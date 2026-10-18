package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/agentproto"
)

const defaultStateDir = "/var/lib/pigger-agent"

// Config is the agent's own settings file: which panel to dial and the secret
// that panel minted for this server.
type Config struct {
	Master   string `json:"master"`
	Secret   string `json:"secret"`
	StateDir string `json:"stateDir,omitempty"`
	// AccessAuditLog enables bounded JSONL connection evidence only on selected hosts.
	AccessAuditLog string `json:"accessAuditLog,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if strings.TrimSpace(cfg.Secret) == "" {
		return cfg, errors.New(path + ": secret is empty")
	}
	if _, err := cfg.connectURL(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultStateDir
	}
	return cfg, nil
}

// connectURL is the panel's agent endpoint: the panel URL, base path included,
// with the WebSocket scheme.
func (c Config) connectURL() (string, error) {
	u, err := url.Parse(strings.TrimSpace(c.Master))
	if err != nil {
		return "", fmt.Errorf("master: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("master must be an http or https URL, got %q", c.Master)
	}
	if u.Host == "" {
		return "", fmt.Errorf("master has no host: %q", c.Master)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.Path += agentproto.ConnectPath
	return u.String(), nil
}
