package core

import (
	"encoding/json"
	"fmt"
	"sort"

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
	buildContext := r.BuildContext
	buildContext.EgressGateways = model.OrderedGateways(cfg.EgressGateways)
	// Display order must not change authentication, routes, or core bytes.
	sort.Slice(buildContext.EgressGateways, func(i, j int) bool {
		return buildContext.EgressGateways[i].ID < buildContext.EgressGateways[j].ID
	})
	inbounds := make([]any, 0, len(cfg.Inbounds))
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
	endpoints, rules := []any{}, []any{}
	for _, g := range buildContext.EgressGateways {
		if !g.Enabled {
			continue
		}
		endpoints = append(endpoints, map[string]any{
			"type": "wireguard", "tag": g.EndpointTag(), "system": false, "mtu": 1408,
			"address": []string{g.TunnelAddress()}, "private_key": g.PrivateKey,
			"peers": []any{map[string]any{"address": g.Server, "port": g.ServerPort, "public_key": g.PeerPublicKey, "allowed_ips": []string{"0.0.0.0/0"}, "persistent_keepalive_interval": 25}},
		})
		users := []string{}
		for _, in := range cfg.Inbounds {
			if in.Enabled {
				users = append(users, protocol.EgressAuthUser(in, g.ID))
			}
		}
		if len(users) > 0 {
			rules = append(rules, map[string]any{"auth_user": users, "action": "resolve", "server": "local", "strategy": "ipv4_only"}, map[string]any{"auth_user": users, "action": "route", "outbound": g.EndpointTag()})
		}
	}
	if len(endpoints) > 0 {
		doc["endpoints"] = endpoints
		doc["dns"] = map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}}
		route["rules"] = rules
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
