package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode"

	"github.com/boltguo/sbm/internal/geo"
	"github.com/boltguo/sbm/internal/model"
)

func DirectAuthUser(in model.Inbound) string { return "direct-" + in.ID }
func EgressAuthUser(in model.Inbound, gatewayID string) string {
	return "egress-" + gatewayID + "-" + in.ID
}

func GatewayLocation(g model.EgressGateway) string {
	if value := strings.TrimSpace(g.LocationOverride); value != "" {
		return value
	}
	if g.Geo.IP == g.Server {
		return geo.LocationName(g.Geo.Country, g.Geo.CountryCode, g.Geo.City)
	}
	return ""
}
func GatewayName(g model.EgressGateway) string {
	return gatewayName(g, "")
}
func gatewayName(g model.EgressGateway, protocolName string) string {
	location, marker := GatewayLocation(g), strings.TrimSpace(g.Marker)
	if location == "" && marker == "" {
		location = "Gateway-" + g.ID
	}
	parts := []string{}
	if location != "" {
		parts = append(parts, location)
	}
	if protocolName != "" {
		parts = append(parts, protocolName)
	}
	if marker != "" {
		parts = append(parts, marker)
	}
	return strings.Join(parts, "-")
}
func EgressVariant(in model.Inbound, g model.EgressGateway) (model.Inbound, bool) {
	variant := in
	for _, c := range in.EgressCredentials {
		if c.GatewayID != g.ID {
			continue
		}
		switch in.Type {
		case TypeVLESSReality:
			if in.VLESS == nil || c.UUID == "" {
				return model.Inbound{}, false
			}
			v := *in.VLESS
			v.UUID = c.UUID
			variant.VLESS = &v
			variant.Name = gatewayName(g, "VLESS")
		case TypeHysteria2:
			if in.Hysteria2 == nil || c.Password == "" {
				return model.Inbound{}, false
			}
			h := *in.Hysteria2
			h.Password = c.Password
			variant.Hysteria2 = &h
			variant.Name = gatewayName(g, "HY2")
		default:
			return model.Inbound{}, false
		}
		return variant, true
	}
	return model.Inbound{}, false
}

// SyncEgressCredentials is used only by configuration mutations, never by a
// render or subscription read. Disabled gateways retain their credentials.
func SyncEgressCredentials(cfg *model.Config) error {
	gateways := make(map[string]bool, len(cfg.EgressGateways))
	for _, g := range cfg.EgressGateways {
		gateways[g.ID] = true
	}
	for i := range cfg.Inbounds {
		in := &cfg.Inbounds[i]
		result := make([]model.EgressCredential, 0, len(gateways))
		seen := make(map[string]bool)
		for _, c := range in.EgressCredentials {
			if !gateways[c.GatewayID] {
				continue
			}
			if seen[c.GatewayID] {
				return errors.New("中继出口凭据重复")
			}
			result = append(result, c)
			seen[c.GatewayID] = true
		}
		for _, g := range cfg.EgressGateways {
			if seen[g.ID] {
				continue
			}
			c := model.EgressCredential{GatewayID: g.ID}
			var err error
			switch in.Type {
			case TypeVLESSReality:
				c.UUID, err = generateUUID()
			case TypeHysteria2:
				c.Password, err = RandomToken(24)
			default:
				return errors.New("不支持中继出口的协议")
			}
			if err != nil {
				return errors.New("生成中继出口凭据失败")
			}
			result = append(result, c)
		}
		in.EgressCredentials = result
	}
	return nil
}
func generateUUID() (string, error) {
	var v [16]byte
	if _, err := rand.Read(v[:]); err != nil {
		return "", err
	}
	v[6] = (v[6] & 0x0f) | 0x40
	v[8] = (v[8] & 0x3f) | 0x80
	s := hex.EncodeToString(v[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

var gatewayIDRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,47}$`)

func ValidateGatewayServer(server string) error {
	addr, err := netip.ParseAddr(server)
	if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return errors.New("中继出口服务器必须是有效的公网 IPv4 地址")
	}
	return nil
}
func validateWireGuardKey(value string) error {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != value {
		return errors.New("WireGuard 密钥必须是 32 字节 Base64")
	}
	return nil
}
func ValidateGateway(g model.EgressGateway) error {
	if !gatewayIDRE.MatchString(g.ID) {
		return errors.New("中继出口 ID 无效")
	}
	if g.TunnelSlot < 1 || g.TunnelSlot > 254 {
		return errors.New("中继出口隧道地址槽无效")
	}
	for _, text := range []string{g.Marker, g.LocationOverride} {
		if len([]rune(text)) > 80 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return errors.New("出口标记和位置不得超过 80 字符或包含控制字符")
		}
	}
	if g.Server != "" || g.Enabled {
		if err := ValidateGatewayServer(g.Server); err != nil {
			return err
		}
	}
	if err := ValidatePort(g.ServerPort); err != nil {
		return errors.New("WireGuard UDP 端口无效")
	}
	for _, key := range []string{g.PrivateKey, g.PeerPublicKey} {
		if key != "" || g.Enabled {
			if err := validateWireGuardKey(key); err != nil {
				return err
			}
		}
	}
	if err := g.TrafficQuota.Validate(); err != nil {
		return err
	}
	return ValidateReset(g.Reset)
}

func ValidateEgress(cfg model.Config) error {
	ids, slots, peers := map[string]bool{}, map[int]bool{}, map[string]bool{}
	for _, g := range cfg.EgressGateways {
		if err := ValidateGateway(g); err != nil {
			return err
		}
		if ids[g.ID] || slots[g.TunnelSlot] {
			return errors.New("中继出口 ID 或隧道地址槽重复")
		}
		ids[g.ID] = true
		slots[g.TunnelSlot] = true
		if g.Server != "" {
			peer := fmt.Sprintf("%s:%d", g.Server, g.ServerPort)
			if peers[peer] {
				return errors.New("中继出口 IPv4 和 UDP 端口组合必须唯一")
			}
			peers[peer] = true
		}
	}
	auths := map[string]bool{}
	for _, in := range cfg.Inbounds {
		auth := DirectAuthUser(in)
		if auths[auth] {
			return errors.New("认证用户重复")
		}
		auths[auth] = true
		seen, values := map[string]bool{}, map[string]bool{}
		switch in.Type {
		case TypeVLESSReality:
			if in.VLESS != nil {
				values[strings.ToLower(in.VLESS.UUID)] = true
			}
		case TypeHysteria2:
			if in.Hysteria2 != nil {
				values[in.Hysteria2.Password] = true
			}
		}
		for _, c := range in.EgressCredentials {
			if !ids[c.GatewayID] || seen[c.GatewayID] {
				return errors.New("中继出口凭据关联无效或重复")
			}
			seen[c.GatewayID] = true
			value := ""
			switch in.Type {
			case TypeVLESSReality:
				if !uuidRE.MatchString(c.UUID) || c.Password != "" {
					return errors.New("中继出口 UUID 无效")
				}
				value = strings.ToLower(c.UUID)
			case TypeHysteria2:
				if len(c.Password) < 8 || len(c.Password) > 128 || c.UUID != "" {
					return errors.New("中继出口密码无效")
				}
				value = c.Password
			}
			if values[value] {
				return errors.New("入站认证凭据必须唯一")
			}
			values[value] = true
			auth := EgressAuthUser(in, c.GatewayID)
			if auths[auth] {
				return errors.New("认证用户重复")
			}
			auths[auth] = true
		}
		if len(seen) != len(ids) {
			return errors.New("缺少中继出口凭据")
		}
	}
	return nil
}
