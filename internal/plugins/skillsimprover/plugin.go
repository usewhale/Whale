package skillsimprover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

type Context struct {
	DataDir       string
	WorkspaceRoot string
}

func StoreFor(ctx Context) *Store {
	return NewStore(ctx.DataDir, ctx.WorkspaceRoot)
}

func Skill() *skills.Skill {
	return &skills.Skill{
		Name:          PluginID,
		Description:   "Review skill usage evidence and draft human-reviewed SKILL.md improvement proposals.",
		When:          "Use when asked to improve, evolve, review, or propose changes to Whale skills.",
		Instructions:  "Inspect current skill files and skills-improver evidence. Draft one concrete proposal at a time. Save proposals with save_skill_proposal. Do not modify SKILL.md directly unless the user explicitly asks you to apply a reviewed change.",
		Path:          "plugin://skills-improver",
		SkillFilePath: "plugin://skills-improver/SKILL.md",
	}
}

func Tools(ctx Context) []core.Tool {
	return []core.Tool{saveProposalTool{store: StoreFor(ctx)}}
}

func Hooks(ctx Context) []agent.HookHandler {
	store := StoreFor(ctx)
	return []agent.HookHandler{
		{
			Event:       agent.HookEventUserPromptSubmit,
			Name:        "skills-improver.capture-feedback",
			Source:      "plugin:skills-improver",
			Description: "Capture explicit user feedback that may improve a skill.",
			Run: func(_ context.Context, payload agent.HookPayload) agent.HookResult {
				ev, ok := promptEvidence(payload)
				if !ok {
					return agent.HookResult{Decision: agent.HookDecisionPass}
				}
				_, _ = store.AppendEvidence(ev)
				return agent.HookResult{Decision: agent.HookDecisionPass}
			},
		},
		{
			Event:       agent.HookEventPostToolUse,
			Name:        "skills-improver.capture-tool-failure",
			Source:      "plugin:skills-improver",
			Description: "Capture failed tool outcomes that may indicate a skill gap.",
			Run: func(_ context.Context, payload agent.HookPayload) agent.HookResult {
				ev, ok := toolFailureEvidence(payload)
				if !ok {
					return agent.HookResult{Decision: agent.HookDecisionPass}
				}
				_, _ = store.AppendEvidence(ev)
				return agent.HookResult{Decision: agent.HookDecisionPass}
			},
		},
		{
			Event:       agent.HookEventStop,
			Name:        "skills-improver.capture-turn-summary",
			Source:      "plugin:skills-improver",
			Description: "Attach the final assistant response to recent skill-improvement evidence.",
			Run: func(_ context.Context, payload agent.HookPayload) agent.HookResult {
				latest, ok := store.LatestUnsummarizedSessionEvidence(payload.SessionID)
				if !ok {
					return agent.HookResult{Decision: agent.HookDecisionPass}
				}
				_, _ = store.AppendEvidence(Evidence{
					Kind:             "turn-summary",
					SessionID:        payload.SessionID,
					Turn:             payload.Turn,
					Skill:            latest.Skill,
					Prompt:           latest.Prompt,
					AssistantSummary: payload.LastAssistantText,
					Metadata: map[string]any{
						"source_evidence_id": latest.ID,
					},
				})
				return agent.HookResult{Decision: agent.HookDecisionPass}
			},
		},
	}
}

type saveProposalTool struct {
	store *Store
}

func (saveProposalTool) Name() string { return "save_skill_proposal" }

func (saveProposalTool) Description() string {
	return "Save a reviewed SKILL.md improvement proposal for later human-approved application."
}

func (saveProposalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill":             map[string]any{"type": "string", "description": "Target skill name."},
			"skill_file_path":   map[string]any{"type": "string", "description": "Filesystem path to the target SKILL.md."},
			"original_sha256":   map[string]any{"type": "string", "description": "SHA-256 of the current target SKILL.md."},
			"summary":           map[string]any{"type": "string", "description": "Short summary of the proposed improvement."},
			"risk":              map[string]any{"type": "string", "description": "Risk or review note for the human reviewer."},
			"proposed_skill_md": map[string]any{"type": "string", "description": "Complete proposed SKILL.md content."},
			"evidence_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"skill", "skill_file_path", "original_sha256", "proposed_skill_md", "summary"},
	}
}

func (saveProposalTool) ReadOnly() bool { return false }

func (saveProposalTool) ApprovalHint() string {
	return "Saves a skills-improver proposal in Whale plugin data. It does not modify SKILL.md."
}

func (t saveProposalTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in Proposal
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return toolError(call, "invalid_args", err.Error()), nil
	}
	p, err := t.store.SaveProposal(in)
	if err != nil {
		return toolError(call, "save_failed", err.Error()), nil
	}
	return toolResult(call, map[string]any{
		"status":          "saved",
		"proposal_id":     p.ID,
		"skill":           p.Skill,
		"skill_file_path": p.SkillFilePath,
		"summary":         p.Summary,
	})
}

func promptEvidence(payload agent.HookPayload) (Evidence, bool) {
	prompt := strings.TrimSpace(payload.Prompt)
	if prompt == "" {
		return Evidence{}, false
	}
	lower := strings.ToLower(prompt)
	keywords := []string{
		"以后", "下次", "记住", "不要", "应该", "纠正", "错了", "不对",
		"remember", "next time", "from now on", "should", "wrong", "incorrect", "prefer",
	}
	hasKeyword := false
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			hasKeyword = true
			break
		}
	}
	skill := firstSkillMention(prompt)
	if !hasKeyword || skill == "" {
		return Evidence{}, false
	}
	return Evidence{
		Kind:      "user-feedback",
		SessionID: payload.SessionID,
		Skill:     skill,
		Prompt:    prompt,
	}, true
}

func toolFailureEvidence(payload agent.HookPayload) (Evidence, bool) {
	result := strings.TrimSpace(payload.ToolResult)
	if result == "" {
		return Evidence{}, false
	}
	msg := ""
	code := payload.ToolErrorCode
	if strings.TrimSpace(payload.ToolOutcome) != "" {
		// Structured channel (phase 2): the outcome decides failure; the
		// first line of the rendered text is the human-readable cause.
		switch core.ToolOutcome(payload.ToolOutcome) {
		case core.OutcomeSuccess, core.OutcomeNoResult:
			return Evidence{}, false
		}
		msg = core.FirstNonEmpty(payload.ToolErrorCode, firstLine(result))
	} else {
		env, ok := core.ParseToolEnvelope(result)
		if !ok || env.Success || env.OK {
			return Evidence{}, false
		}
		msg = core.FirstNonEmpty(env.Error, env.Message, env.Code)
		code = env.Code
	}
	return Evidence{
		Kind:              "tool-failure",
		SessionID:         payload.SessionID,
		Skill:             skillFromToolArgs(payload.ToolArgs),
		ToolName:          payload.ToolName,
		ToolArgsSummary:   summarizeJSON(payload.ToolArgs),
		ToolResultSummary: core.FirstNonEmpty(msg, result),
		Metadata: map[string]any{
			"code": code,
		},
	}, true
}

func firstSkillMention(text string) string {
	fields := strings.Fields(text)
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}<>`\"'")
		if strings.HasPrefix(f, "$") {
			name := strings.TrimPrefix(f, "$")
			if skills.ValidName(name) {
				return name
			}
		}
	}
	return ""
}

func skillFromToolArgs(args any) string {
	m, ok := args.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"skill", "name"} {
		if v, _ := m[key].(string); skills.ValidName(v) {
			return v
		}
	}
	return ""
}

func summarizeJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func toolResult(call core.ToolCall, data map[string]any) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(data))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func toolError(call core.ToolCall, code, msg string) core.ToolResult {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, msg))
	if err != nil {
		content = `{"success":false,"code":"tool_error","error":"failed to marshal tool error"}`
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

func SortEvidence(evs []Evidence) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].CreatedAt.After(evs[j].CreatedAt) })
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
