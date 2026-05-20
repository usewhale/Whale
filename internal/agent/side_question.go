package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
)

type SideQuestionEventType string

const (
	SideQuestionEventDelta SideQuestionEventType = "delta"
	SideQuestionEventDone  SideQuestionEventType = "done"
	SideQuestionEventError SideQuestionEventType = "error"
)

type SideQuestionEvent struct {
	Type    SideQuestionEventType
	Content string
	Err     error
}

func (a *Agent) RunSideQuestion(ctx context.Context, sessionID, question string) (<-chan SideQuestionEvent, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("Usage: /btw <your question>")
	}
	if spent, blocked := a.budgetExceeded(sessionID); blocked {
		return nil, fmt.Errorf("%w: spent $%.6f >= cap $%.6f", ErrBudgetExceeded, spent, a.budgetWarningUSD)
	}
	history, err := a.store.List(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	_, sessionActive := a.active.Load(sessionID)
	history = stripInProgressAssistantMessage(history, sessionActive)
	rt := memory.HydrateRuntime(memory.NewImmutablePrefix(a.buildImmutableSystemBlocks()), history)
	tmpHistory := append(a.buildTurnProviderHistory(sessionID, rt), core.Message{
		SessionID: sessionID,
		Role:      core.RoleUser,
		Text:      sideQuestionPrompt(question),
	})
	out := make(chan SideQuestionEvent, 32)
	go func() {
		defer close(out)
		var response strings.Builder
		sawToolUse := false
		lastUsage := llm.Usage{}
		lastModel := ""
		for ev := range a.provider.StreamResponse(ctx, tmpHistory, nil) {
			switch ev.Type {
			case llm.EventContentDelta:
				if ev.Content != "" {
					response.WriteString(ev.Content)
					out <- SideQuestionEvent{Type: SideQuestionEventDelta, Content: ev.Content}
				}
			case llm.EventToolUseStart, llm.EventToolUseStop, llm.EventToolArgsDelta:
				sawToolUse = true
			case llm.EventComplete:
				if ev.Response != nil {
					lastUsage = ev.Response.Usage
					lastModel = ev.Response.Model
					if len(ev.Response.ToolCalls) > 0 {
						sawToolUse = true
					}
					if strings.TrimSpace(ev.Response.Content) != "" {
						response.Reset()
						response.WriteString(ev.Response.Content)
					}
				}
			case llm.EventError:
				if ev.Err != nil {
					out <- SideQuestionEvent{Type: SideQuestionEventError, Err: ev.Err, Content: "(API error: " + ev.Err.Error() + ")"}
					return
				}
			}
		}
		a.recordTurnCost(sessionID, lastUsage, lastModel, rt.Prefix.Fingerprint())
		text := strings.TrimSpace(response.String())
		if sawToolUse && text == "" {
			text = "(The model tried to call a tool instead of answering directly. Try rephrasing or ask in the main conversation.)"
		}
		if text == "" {
			text = "No response received"
		}
		out <- SideQuestionEvent{Type: SideQuestionEventDone, Content: text}
	}()
	return out, nil
}

func stripInProgressAssistantMessage(history []core.Message, sessionActive bool) []core.Message {
	out := append([]core.Message(nil), history...)
	if len(out) == 0 {
		return out
	}
	last := out[len(out)-1]
	if last.Role != core.RoleAssistant {
		return out
	}
	if last.FinishReason == core.FinishReasonToolUse {
		return out[:len(out)-1]
	}
	if last.FinishReason == "" && (sessionActive || strings.TrimSpace(last.Text) == "") {
		return out[:len(out)-1]
	}
	return out
}

func sideQuestionPrompt(question string) string {
	return strings.TrimSpace(`<system-reminder>This is a side question from the user. You must answer this question directly in a single response.

IMPORTANT CONTEXT:
- You are a separate, lightweight agent spawned to answer this one question
- The main agent is NOT interrupted - it continues working independently in the background
- You share the conversation context but are a completely separate instance
- Do NOT reference being interrupted or what you were "previously doing" - that framing is incorrect

CRITICAL CONSTRAINTS:
- You have NO tools available - you cannot read files, run commands, search, or take any actions
- This is a one-off response - there will be no follow-up turns
- You can ONLY provide information based on what you already know from the conversation context
- NEVER say things like "Let me try...", "I'll now...", "Let me check...", or promise to take any action
- If you don't know the answer, say so - do not offer to look it up or investigate

Simply answer the question with the information you have.</system-reminder>`) + "\n\n" + strings.TrimSpace(question)
}
