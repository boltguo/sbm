package store

import (
	"fmt"
	"sync"

	"github.com/boltguo/sbm/internal/model"
)

type ConfigStore struct {
	file   *JSONFile[model.Config]
	mu     sync.RWMutex
	config model.Config
}

func OpenConfig(path string) (*ConfigStore, error) {
	f := NewJSONFile[model.Config](path)
	cfg, err := f.Load()
	if err != nil {
		return nil, err
	}
	if cfg.Version != model.ConfigVersion {
		return nil, fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	return &ConfigStore{file: f, config: cfg}, nil
}

func NewConfigStore(path string, cfg model.Config) *ConfigStore {
	return &ConfigStore{file: NewJSONFile[model.Config](path), config: cfg}
}

func (s *ConfigStore) Get() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.config)
}

func (s *ConfigStore) Replace(cfg model.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.file.Save(cfg); err != nil {
		return err
	}
	s.config = cloneConfig(cfg)
	return nil
}

func cloneConfig(cfg model.Config) model.Config {
	copyCfg := cfg
	if cfg.WireGuardExit != nil {
		wireGuardExit := *cfg.WireGuardExit
		copyCfg.WireGuardExit = &wireGuardExit
	}
	copyCfg.Inbounds = append([]model.Inbound(nil), cfg.Inbounds...)
	for i := range copyCfg.Inbounds {
		if cfg.Inbounds[i].VLESS != nil {
			v := *cfg.Inbounds[i].VLESS
			copyCfg.Inbounds[i].VLESS = &v
		}
		if cfg.Inbounds[i].Hysteria2 != nil {
			h := *cfg.Inbounds[i].Hysteria2
			copyCfg.Inbounds[i].Hysteria2 = &h
		}
	}
	return copyCfg
}
