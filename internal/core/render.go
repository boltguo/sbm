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
	inbounds := make([]any, 0, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		driver, _ := r.Registry.Get(inbound.Type)
		built, err := driver.Build(inbound, r.BuildContext)
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
