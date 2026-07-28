package protocol

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

const (
	TypeVLESSReality = "vless-reality"
	TypeHysteria2    = "hysteria2"
)

type Driver interface {
	Type() string
	Network() string
	Validate(model.Inbound) error
	Build(model.Inbound, BuildContext) (map[string]any, error)
	ShareLink(model.Inbound, ShareContext) (string, error)
}

type BuildContext struct{ CertificatePath, KeyPath string }
type ShareContext struct{ Domain string }

type Registry struct{ drivers map[string]Driver }

func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{drivers: make(map[string]Driver)}
	for _, d := range drivers {
		r.drivers[d.Type()] = d
	}
	return r
}

func DefaultRegistry() *Registry { return NewRegistry(VLESSDriver{}, Hysteria2Driver{}) }

func (r *Registry) Get(kind string) (Driver, bool) { d, ok := r.drivers[kind]; return d, ok }

func (r *Registry) ValidateConfig(cfg model.Config) error {
	if cfg.Version != model.ConfigVersion {
		return fmt.Errorf("配置版本必须为 %d", model.ConfigVersion)
	}
	if err := ValidateDomain(cfg.Domain); err != nil {
		return err
	}
	if err := ValidatePort(cfg.PanelPort); err != nil {
		return fmt.Errorf("面板端口: %w", err)
	}
	if cfg.AdminUsername == "" || len(cfg.AdminUsername) > 64 {
		return errors.New("管理员用户名无效")
	}
	if len(cfg.SessionSecret) < 43 || len(cfg.ClashAPISecret) < 43 || len(cfg.SubscriptionToken) < 43 {
		return errors.New("安全密钥长度不足")
	}
	if cfg.TotalBytes < 0 {
		return errors.New("总流量不能为负数")
	}
	if err := ValidateReset(cfg.Reset); err != nil {
		return err
	}
	if err := ValidateOutboundStrategy(cfg.OutboundStrategy); err != nil {
		return err
	}
	type endpoint struct {
		id      string
		port    int
		network string
	}
	seen := make(map[string]endpoint)
	seenIDs := make(map[string]bool)
	for _, inbound := range cfg.Inbounds {
		driver, ok := r.Get(inbound.Type)
		if !ok {
			return fmt.Errorf("不支持的协议类型 %q", inbound.Type)
		}
		if strings.TrimSpace(inbound.ID) == "" || len(inbound.ID) > 64 {
			return errors.New("协议 ID 无效")
		}
		if seenIDs[inbound.ID] {
			return errors.New("协议 ID 重复")
		}
		seenIDs[inbound.ID] = true
		if len([]rune(strings.TrimSpace(inbound.Name))) == 0 || len([]rune(inbound.Name)) > 80 {
			return errors.New("节点名称长度必须为 1 到 80 个字符")
		}
		if err := driver.Validate(inbound); err != nil {
			return fmt.Errorf("%s: %w", inbound.Name, err)
		}
		key := driver.Network() + ":" + strconv.Itoa(inbound.Port)
		if old, exists := seen[key]; exists {
			return fmt.Errorf("端口冲突：%s 与 %s 都使用 %s/%d", old.id, inbound.ID, driver.Network(), inbound.Port)
		}
		seen[key] = endpoint{inbound.ID, inbound.Port, driver.Network()}
		if driver.Network() == "tcp" && inbound.Port == cfg.PanelPort {
			return fmt.Errorf("TCP/%d 与面板端口冲突", cfg.PanelPort)
		}
	}
	return nil
}

func ValidateOutboundStrategy(strategy string) error {
	switch strategy {
	case "", model.OutboundStrategyAuto, model.OutboundStrategyPreferIPv4, model.OutboundStrategyPreferIPv6, model.OutboundStrategyIPv4Only, model.OutboundStrategyIPv6Only:
		return nil
	default:
		return errors.New("出站地址策略无效")
	}
}

func ValidateReset(reset model.ResetConfig) error {
	if reset.Mode != "none" && reset.Mode != "monthly" {
		return errors.New("重置模式只能是 none 或 monthly")
	}
	if reset.Day < 1 || reset.Day > 28 {
		return errors.New("每月重置日期必须为 1 到 28")
	}
	if reset.Timezone == "" {
		return errors.New("时区不能为空")
	}
	if _, err := time.LoadLocation(reset.Timezone); err != nil {
		return errors.New("时区无效")
	}
	return nil
}

var hostnameRE = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateDomain(value string) error {
	if len(value) > 253 || !hostnameRE.MatchString(value) || net.ParseIP(value) != nil {
		return errors.New("域名格式无效")
	}
	return nil
}

func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("端口必须在 1 到 65535 之间")
	}
	return nil
}

type VLESSDriver struct{}

func (VLESSDriver) Type() string    { return TypeVLESSReality }
func (VLESSDriver) Network() string { return "tcp" }
func (VLESSDriver) Validate(in model.Inbound) error {
	if err := ValidatePort(in.Port); err != nil {
		return err
	}
	if in.VLESS == nil {
		return errors.New("缺少 VLESS 参数")
	}
	v := in.VLESS
	if !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(v.UUID) {
		return errors.New("UUID 格式无效")
	}
	if err := ValidateDomain(v.SNI); err != nil {
		return fmt.Errorf("Reality SNI: %w", err)
	}
	if v.PrivateKey == "" || v.PublicKey == "" {
		return errors.New("Reality 密钥不能为空")
	}
	if !regexp.MustCompile(`(?i)^[0-9a-f]{2,16}$`).MatchString(v.ShortID) || len(v.ShortID)%2 != 0 {
		return errors.New("short ID 必须为 2 到 16 位偶数长度十六进制")
	}
	return nil
}
func (VLESSDriver) Build(in model.Inbound, _ BuildContext) (map[string]any, error) {
	if err := (VLESSDriver{}).Validate(in); err != nil {
		return nil, err
	}
	v := in.VLESS
	return map[string]any{
		"type": "vless", "tag": "in-" + in.ID, "listen": "::", "listen_port": in.Port,
		"users": []any{map[string]any{"uuid": v.UUID, "flow": "xtls-rprx-vision"}},
		"tls": map[string]any{"enabled": true, "server_name": v.SNI, "reality": map[string]any{
			"enabled": true, "handshake": map[string]any{"server": v.SNI, "server_port": 443},
			"private_key": v.PrivateKey, "short_id": []string{v.ShortID},
		}},
	}, nil
}
func (VLESSDriver) ShareLink(in model.Inbound, ctx ShareContext) (string, error) {
	if err := (VLESSDriver{}).Validate(in); err != nil {
		return "", err
	}
	v := in.VLESS
	query := url.Values{"encryption": {"none"}, "flow": {"xtls-rprx-vision"}, "security": {"reality"}, "sni": {v.SNI}, "fp": {"chrome"}, "pbk": {v.PublicKey}, "sid": {v.ShortID}, "type": {"tcp"}}
	u := &url.URL{Scheme: "vless", User: url.User(v.UUID), Host: net.JoinHostPort(ctx.Domain, strconv.Itoa(in.Port)), RawQuery: query.Encode(), Fragment: in.Name}
	u.RawFragment = url.PathEscape(in.Name)
	return u.String(), nil
}

type Hysteria2Driver struct{}

func (Hysteria2Driver) Type() string    { return TypeHysteria2 }
func (Hysteria2Driver) Network() string { return "udp" }
func (Hysteria2Driver) Validate(in model.Inbound) error {
	if err := ValidatePort(in.Port); err != nil {
		return err
	}
	if in.Hysteria2 == nil {
		return errors.New("缺少 Hysteria2 参数")
	}
	if len(in.Hysteria2.Password) < 8 || len(in.Hysteria2.Password) > 128 {
		return errors.New("Hysteria2 密码长度必须为 8 到 128")
	}
	if in.Hysteria2.Obfs != "" && in.Hysteria2.Obfs != "salamander" {
		return errors.New("只支持 salamander 混淆")
	}
	if in.Hysteria2.Obfs != "" && len(in.Hysteria2.ObfsPassword) < 8 {
		return errors.New("混淆密码至少 8 个字符")
	}
	return nil
}
func (Hysteria2Driver) Build(in model.Inbound, ctx BuildContext) (map[string]any, error) {
	if err := (Hysteria2Driver{}).Validate(in); err != nil {
		return nil, err
	}
	h := in.Hysteria2
	result := map[string]any{
		"type": "hysteria2", "tag": "in-" + in.ID, "listen": "::", "listen_port": in.Port,
		"users": []any{map[string]any{"password": h.Password}},
		"tls":   map[string]any{"enabled": true, "alpn": []string{"h3"}, "certificate_path": ctx.CertificatePath, "key_path": ctx.KeyPath},
	}
	if h.Obfs != "" {
		result["obfs"] = map[string]any{"type": h.Obfs, "password": h.ObfsPassword}
	}
	return result, nil
}
func (Hysteria2Driver) ShareLink(in model.Inbound, ctx ShareContext) (string, error) {
	if err := (Hysteria2Driver{}).Validate(in); err != nil {
		return "", err
	}
	h := in.Hysteria2
	query := url.Values{"sni": {ctx.Domain}, "alpn": {"h3"}}
	if h.Obfs != "" {
		query.Set("obfs", h.Obfs)
		query.Set("obfs-password", h.ObfsPassword)
	}
	u := &url.URL{Scheme: "hysteria2", User: url.User(h.Password), Host: net.JoinHostPort(ctx.Domain, strconv.Itoa(in.Port)), RawQuery: query.Encode(), Fragment: in.Name}
	u.RawFragment = url.PathEscape(in.Name)
	return u.String(), nil
}

func RandomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func RandomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type RealityKeys struct{ Private, Public string }

type KeyGenerator interface {
	UUID(context.Context) (string, error)
	Reality(context.Context) (RealityKeys, error)
}

type SingBoxKeyGenerator struct{ Binary string }

func (g SingBoxKeyGenerator) UUID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, g.Binary, "generate", "uuid").Output()
	if err != nil {
		return "", fmt.Errorf("生成 UUID 失败: %w", err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", errors.New("sing-box 返回了空 UUID")
	}
	return value, nil
}
func (g SingBoxKeyGenerator) Reality(ctx context.Context) (RealityKeys, error) {
	out, err := exec.CommandContext(ctx, g.Binary, "generate", "reality-keypair").CombinedOutput()
	if err != nil {
		return RealityKeys{}, fmt.Errorf("生成 Reality 密钥失败: %w", err)
	}
	var keys RealityKeys
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, ":", " "))
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "privatekey", "private_key", "private":
			keys.Private = fields[len(fields)-1]
		case "publickey", "public_key", "public":
			keys.Public = fields[len(fields)-1]
		}
	}
	if keys.Private == "" || keys.Public == "" {
		return RealityKeys{}, errors.New("无法解析 sing-box Reality 密钥输出")
	}
	return keys, nil
}
