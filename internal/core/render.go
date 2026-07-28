package core

import (
	"encoding/json"
	"fmt"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

type Renderer struct {
	Registry     *protocol.Registry
	BuildContext protocol.BuildContext
}

func (r Renderer) Render(cfg model.Config) ([]byte, error) {
	if err := r.Registry.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	wireGuardEnabled := cfg.WireGuardExit != nil && cfg.WireGuardExit.Enabled
	buildContext := r.BuildContext
	buildContext.WireGuardExitEnabled = wireGuardEnabled
	inbounds := make([]any, 0, len(cfg.Inbounds))
	wireGuardAuthUsers := make([]string, 0, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		driver, _ := r.Registry.Get(inbound.Type)
		built, err := driver.Build(inbound, buildContext)
		if err != nil {
			return nil, fmt.Errorf("生成 %s 配置: %w", inbound.Name, err)
		}
		inbounds = append(inbounds, built)
		if wireGuardEnabled && protocol.HasWireGuardExitCredential(inbound) {
			wireGuardAuthUsers = append(wireGuardAuthUsers, protocol.WireGuardAuthUser(inbound))
		}
	}
	direct := map[string]any{"type": "direct", "tag": "direct"}
	route := map[string]any{"rules": []any{}, "final": "direct"}
	doc := map[string]any{
		"log":       map[string]any{"level": "warn", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []any{direct},
		"route":     route,
		"experimental": map[string]any{"clash_api": map[string]any{
			"external_controller": "127.0.0.1:9090", "secret": cfg.ClashAPISecret,
		}},
	}
	if wireGuardEnabled {
		exit := cfg.WireGuardExit
		peer := map[string]any{
			"address":                       exit.Server,
			"port":                          exit.ServerPort,
			"public_key":                    exit.PeerPublicKey,
			"allowed_ips":                   []string{"0.0.0.0/0"},
			"persistent_keepalive_interval": model.WireGuardExitPersistentKeepalive,
		}
		doc["endpoints"] = []any{map[string]any{
			"type":        "wireguard",
			"tag":         "exit-wireguard",
			"system":      false,
			"mtu":         model.WireGuardExitMTU,
			"address":     []string{model.WireGuardExitLocalAddress},
			"private_key": exit.PrivateKey,
			"peers":       []any{peer},
		}}
		doc["dns"] = map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}}
		if len(wireGuardAuthUsers) > 0 {
			route["rules"] = []any{
				map[string]any{
					"auth_user": wireGuardAuthUsers,
					"action":    "resolve",
					"server":    "local",
					"strategy":  model.OutboundStrategyIPv4Only,
				},
				map[string]any{
					"auth_user": wireGuardAuthUsers,
					"action":    "route",
					"outbound":  "exit-wireguard",
				},
			}
		}
	}
	if cfg.OutboundStrategy != "" && cfg.OutboundStrategy != model.OutboundStrategyAuto {
		doc["dns"] = map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}}
		direct["domain_resolver"] = map[string]any{"server": "local", "strategy": cfg.OutboundStrategy}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
