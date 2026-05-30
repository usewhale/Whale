package defaults

import "strings"

const (
	DefaultModel                 = "deepseek-v4-flash"
	ProModel                     = "deepseek-v4-pro"
	DefaultReasoningEffort       = "high"
	DefaultThinkingEnabled       = true
	DefaultContextWindow         = 128_000
	DeepSeekV4ContextWindow      = 1_000_000
	DefaultAutoCompactThreshold  = 0.85
	DefaultAgentCompactThreshold = 0.90
	DefaultMemoryMaxChars        = 8000
	DefaultMemoryFileOrderCSV    = "AGENTS.md,.claude/instructions.md,CLAUDE.md"
)

var supportedModels = []string{
	DefaultModel,
	ProModel,
}

var defaultMemoryFileOrder = []string{
	"AGENTS.md",
	".claude/instructions.md",
	"CLAUDE.md",
}

func SupportedModels() []string {
	return append([]string(nil), supportedModels...)
}

func IsSupportedModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, supported := range supportedModels {
		if m == supported {
			return true
		}
	}
	// Allow any model for custom API endpoints
	return true
}

func DefaultMemoryFileOrder() []string {
	return append([]string(nil), defaultMemoryFileOrder...)
}

func IsDeepSeekV4Model(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, DefaultModel) || strings.Contains(m, ProModel)
}

// ContextWindowForModel returns the context window size in tokens for model.
func ContextWindowForModel(model string) int {
	if strings.TrimSpace(model) == "" {
		return DefaultContextWindow
	}
	if IsDeepSeekV4Model(model) {
		return DeepSeekV4ContextWindow
	}
	// Use large context window for custom models
	return DeepSeekV4ContextWindow
}
