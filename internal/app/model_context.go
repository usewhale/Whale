package app

import "github.com/usewhale/whale/internal/defaults"

// contextWindowForModel returns the context window size (in tokens) for the given
// model name. DeepSeek V4 models get 1M; everything else gets the default 128K.
func contextWindowForModel(model string) int {
	return defaults.ContextWindowForModel(model)
}
