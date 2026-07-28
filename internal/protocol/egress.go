package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/boltguo/sbm/internal/model"
)

const wireGuardAuthPrefix = "exit-wireguard-"

func WireGuardAuthUser(in model.Inbound) string {
	return wireGuardAuthPrefix + in.ID
}

func DirectAuthUser(in model.Inbound) string {
	return "direct-" + in.ID
}

func EnsureWireGuardExitCredential(in *model.Inbound) error {
	switch in.Type {
	case TypeVLESSReality:
		if in.VLESS == nil {
			return nil
		}
		if in.VLESS.WireGuardExitUUID == "" {
			uuid, err := generateUUID()
			if err != nil {
				return err
			}
			in.VLESS.WireGuardExitUUID = uuid
		}
	case TypeHysteria2:
		if in.Hysteria2 == nil {
			return nil
		}
		if in.Hysteria2.WireGuardExitPassword == "" {
			password, err := RandomToken(24)
			if err != nil {
				return err
			}
			in.Hysteria2.WireGuardExitPassword = password
		}
	}
	return nil
}

func HasWireGuardExitCredential(in model.Inbound) bool {
	switch in.Type {
	case TypeVLESSReality:
		return in.VLESS != nil && in.VLESS.WireGuardExitUUID != ""
	case TypeHysteria2:
		return in.Hysteria2 != nil && in.Hysteria2.WireGuardExitPassword != ""
	default:
		return false
	}
}

func WireGuardExitVariant(in model.Inbound, label string) (model.Inbound, bool) {
	variant := in
	variant.Name = fmt.Sprintf("%s · via %s", in.Name, WireGuardExitLabel(label))
	switch in.Type {
	case TypeVLESSReality:
		if in.VLESS == nil || in.VLESS.WireGuardExitUUID == "" {
			return model.Inbound{}, false
		}
		options := *in.VLESS
		options.UUID = options.WireGuardExitUUID
		variant.VLESS = &options
	case TypeHysteria2:
		if in.Hysteria2 == nil || in.Hysteria2.WireGuardExitPassword == "" {
			return model.Inbound{}, false
		}
		options := *in.Hysteria2
		options.Password = options.WireGuardExitPassword
		variant.Hysteria2 = &options
	default:
		return model.Inbound{}, false
	}
	return variant, true
}

func WireGuardExitLabel(label string) string {
	if value := strings.TrimSpace(label); value != "" {
		return value
	}
	return model.DefaultWireGuardExitConfig().Label
}

func generateUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成 WireGuard 出口节点 UUID 失败: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
