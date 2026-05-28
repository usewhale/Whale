package telemetry

import (
	"math"
	"testing"

	"github.com/usewhale/whale/internal/llm"
)

func TestEstimateTurnUSD_FlashVsPro(t *testing.T) {
	u := llm.Usage{
		PromptTokens:         1_000_000,
		PromptCacheHitTokens: 100_000,
		CompletionTokens:     100_000,
	}
	flash := EstimateTurnUSD("deepseek-v4-flash", u)
	pro := EstimateTurnUSD("deepseek-v4-pro", u)
	if math.Abs(flash-0.15428) > 0.0000001 {
		t.Fatalf("unexpected flash cost: %f", flash)
	}
	if math.Abs(pro-0.4788625) > 0.0000001 {
		t.Fatalf("unexpected pro cost: %f", pro)
	}
}

func TestEstimateCacheSavingsUSD(t *testing.T) {
	got := EstimateCacheSavingsUSD("deepseek-v4-flash", 1_000_000)
	want := 0.1372
	if math.Abs(got-want) > 0.0000001 {
		t.Fatalf("unexpected cache savings: %.9f, want %.9f", got, want)
	}
}

func TestBuildTurnStats_ReasoningReplayAndCacheRatio(t *testing.T) {
	u := llm.Usage{
		PromptCacheHitTokens:  300,
		PromptCacheMissTokens: 100,
		ReasoningReplayTokens: 42,
	}
	st := BuildTurnStats(3, "deepseek-v4-flash", u)
	if st.Turn != 3 {
		t.Fatalf("unexpected turn: %d", st.Turn)
	}
	if st.ReasoningReplayTok != 42 {
		t.Fatalf("unexpected replay tokens: %d", st.ReasoningReplayTok)
	}
	if st.CacheHitRatio != 0.75 {
		t.Fatalf("unexpected cache hit ratio: %f", st.CacheHitRatio)
	}
}
