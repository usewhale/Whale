package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/securefs"
	"github.com/usewhale/whale/internal/store"
)

type hookStateFile struct {
	Hooks agent.HookStates `json:"hooks"`
}

func HookStatePath(dataDir, workspaceRoot string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = store.DefaultDataDir()
	}
	return filepath.Join(dataDir, "hooks", plugins.WorkspaceHash(workspaceRoot)+".json")
}

func LoadHookStates(dataDir, workspaceRoot string) (agent.HookStates, error) {
	path := HookStatePath(dataDir, workspaceRoot)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return agent.HookStates{}, nil
		}
		return nil, fmt.Errorf("read hook state %s: %w", path, err)
	}
	var raw hookStateFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse hook state %s: %w", path, err)
	}
	if len(raw.Hooks) == 0 {
		return agent.HookStates{}, nil
	}
	return raw.Hooks, nil
}

func SaveHookStates(dataDir, workspaceRoot string, states agent.HookStates) error {
	path := HookStatePath(dataDir, workspaceRoot)
	body, err := json.MarshalIndent(hookStateFile{Hooks: states}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hook state: %w", err)
	}
	body = append(body, '\n')
	if err := securefs.WritePrivateFile(path, body); err != nil {
		return fmt.Errorf("write hook state: %w", err)
	}
	return nil
}
