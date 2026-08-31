package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/boltguo/sbm/internal/model"
)

type ConfigStore struct {
	file   *JSONFile[model.Config]
	mu     sync.RWMutex
	config model.Config
}

func OpenConfig(path string) (*ConfigStore, error) {
	version, err := readConfigVersion(path)
	if err != nil {
		version, _ = readConfigVersion(path + ".bak")
	}
	if version != 0 && version != model.ConfigVersion {
		return nil, fmt.Errorf("unsupported config version %d", version)
	}
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

func readConfigVersion(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return 0, err
	}
	return header.Version, nil
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
