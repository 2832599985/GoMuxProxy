package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.UpstreamProxy); err != nil {
		return fmt.Errorf("invalid upstream_proxy %q: %w", c.UpstreamProxy, err)
	}
	seen := map[string]bool{}
	for i, l := range c.Listeners {
		if _, _, err := net.SplitHostPort(l.Address); err != nil {
			return fmt.Errorf("listeners[%d]: invalid address %q: %w", i, l.Address, err)
		}
		if seen[l.Address] {
			return fmt.Errorf("listeners[%d]: duplicate address %s", i, l.Address)
		}
		seen[l.Address] = true
		switch l.Protocol {
		case ProtoSocks5, ProtoHTTP, ProtoMixed:
		default:
			return fmt.Errorf("listeners[%d]: unknown protocol %q", i, l.Protocol)
		}
	}
	return nil
}
