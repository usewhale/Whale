package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultConfigFile = "mcp.json"

type Config struct {
	Servers map[string]ServerConfig
	Path    string
}

type ServerConfig struct {
	Name          string            `json:"-"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Type          string            `json:"type,omitempty"`
	Transport     string            `json:"transport,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	DisabledTools []string          `json:"disabled_tools,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
}

func DefaultConfigPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return DefaultConfigFile
	}
	return filepath.Join(dataDir, DefaultConfigFile)
}

func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	cfg := Config{Servers: map[string]ServerConfig{}, Path: path}
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	var raw struct {
		Servers    map[string]ServerConfig `json:"servers"`
		MCPServers map[string]ServerConfig `json:"mcpServers"`
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(b, &raw); err != nil {
		return cfg, fmt.Errorf("parse mcp config: %w", err)
	}
	mergeServers(cfg.Servers, raw.Servers)
	mergeServers(cfg.Servers, raw.MCPServers)
	return cfg, nil
}

func mergeServers(dst map[string]ServerConfig, src map[string]ServerConfig) {
	for name, srv := range src {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		srv.Name = name
		dst[name] = srv
	}
}

func (s ServerConfig) TimeoutDuration() time.Duration {
	if s.Timeout <= 0 {
		return 15 * time.Second
	}
	return time.Duration(s.Timeout) * time.Second
}

func (s ServerConfig) transportKind() (string, error) {
	kindFromType, hasType, err := normalizeTransport(s.Type)
	if err != nil {
		return "", fmt.Errorf("invalid type: %w", err)
	}
	kindFromTransport, hasTransport, err := normalizeTransport(s.Transport)
	if err != nil {
		return "", fmt.Errorf("invalid transport: %w", err)
	}
	if hasType && hasTransport && kindFromType != kindFromTransport {
		return "", fmt.Errorf("conflicting type %q and transport %q", s.Type, s.Transport)
	}
	if hasType {
		return kindFromType, nil
	}
	if hasTransport {
		return kindFromTransport, nil
	}
	if strings.TrimSpace(s.URL) != "" {
		return "http", nil
	}
	if strings.TrimSpace(s.Command) != "" {
		return "stdio", nil
	}
	return "", fmt.Errorf("requires either command or url")
}

func normalizeTransport(value string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, nil
	}
	switch strings.ToLower(value) {
	case "stdio":
		return "stdio", true, nil
	case "http", "streamable-http", "streamable_http", "streamablehttp":
		return "http", true, nil
	default:
		return "", true, fmt.Errorf("unsupported transport %q", value)
	}
}

func (s ServerConfig) disabledToolSet() map[string]bool {
	out := make(map[string]bool, len(s.DisabledTools))
	for _, name := range s.DisabledTools {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func expandEnvRefs(value string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			out.WriteString(value)
			return out.String(), nil
		}
		out.WriteString(value[:start])
		rest := value[start+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return "", fmt.Errorf("unclosed environment reference")
		}
		name := strings.TrimSpace(rest[:end])
		if name == "" {
			return "", fmt.Errorf("empty environment reference")
		}
		resolved, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", name)
		}
		out.WriteString(resolved)
		value = rest[end+1:]
	}
}

func resolvedEnvPairs(env map[string]string) ([]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v, err := expandEnvRefs(env[k])
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out = append(out, k+"="+v)
	}
	return out, nil
}

func resolvedHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if !validHeaderName(name) {
			return nil, fmt.Errorf("invalid header name %q", name)
		}
		resolved, err := expandEnvRefs(value)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		if strings.ContainsAny(resolved, "\r\n") {
			return nil, fmt.Errorf("header %q contains invalid newline", name)
		}
		out[name] = resolved
	}
	return out, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}
