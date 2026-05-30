package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func resolveDeepSeekAPIKey(dataDir string) (string, apiKeySource, error) {
	if v := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); v != "" {
		return v, apiKeySourceEnv, nil
	}
	creds, err := LoadCredentials(dataDir)
	if err != nil {
		return "", apiKeySourceMissing, err
	}
	if v := strings.TrimSpace(creds.DeepSeekAPIKey); v != "" {
		return v, apiKeySourceCredentials, nil
	}
	return "", apiKeySourceMissing, nil
}

func readCredentialsState(dataDir string) fileState {
	path := credentialsPath(dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileState{Path: path}
		}
		return fileState{Path: path, Present: true, Err: err}
	}
	var creds Credentials
	if err := json.Unmarshal(b, &creds); err != nil {
		return fileState{Path: path, Present: true, Err: fmt.Errorf("unmarshal credentials: %w", err)}
	}
	return fileState{Path: path, Present: true}
}

func tailKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if len(trimmed) <= 4 {
		return trimmed
	}
	return "…" + trimmed[len(trimmed)-4:]
}
