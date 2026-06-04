package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

const userSimSystemPrompt = `You are roleplaying a user contacting a retail support agent.
Rules:
- Stay in character; never break the fourth wall.
- Never reveal you are an AI or mention any system.
- Pursue the goal. Do not volunteer facts the agent hasn't asked for.
- Keep replies to one or two sentences.
- When the goal is clearly met OR clearly refused by the agent, output ONLY the literal token: ##STOP##
- Do not output ##STOP## on your first message; give the agent a chance.`

type userSimulator struct {
	provider llm.Provider
	persona  userPersona
	model    string
}

func newUserSimulator(provider llm.Provider, persona userPersona, model string) userSimulator {
	return userSimulator{provider: provider, persona: persona, model: model}
}

func (s userSimulator) next(ctx context.Context, transcript []turn) (string, bool, error) {
	knowns, _ := json.MarshalIndent(s.persona.Knowns, "", "  ")
	messages := []core.Message{{
		Role: core.RoleSystem,
		Text: fmt.Sprintf("%s\n\nCharacter: %s\nGoal: %s\nFacts you may share when asked, but do not volunteer all at once:\n%s",
			userSimSystemPrompt, s.persona.Style, s.persona.Goal, string(knowns)),
	}}
	if len(transcript) == 0 {
		messages = append(messages, core.Message{
			Role: core.RoleUser,
			Text: "Write your opening message to the support agent. One or two sentences. Do not dump all the facts.",
		})
	} else {
		messages = append(messages, core.Message{
			Role: core.RoleUser,
			Text: fmt.Sprintf("Here is the conversation so far. You are the USER.\n\n%s\n\nWrite ONLY your next user reply, or output ##STOP## if the goal is clearly met or clearly refused.",
				transcriptToString(transcript)),
		})
	}

	var text strings.Builder
	for ev := range s.provider.StreamResponse(ctx, messages, nil) {
		switch ev.Type {
		case llm.EventContentDelta:
			text.WriteString(ev.Content)
		case llm.EventComplete:
			if ev.Response != nil && strings.TrimSpace(ev.Response.Content) != "" {
				text.Reset()
				text.WriteString(ev.Response.Content)
			}
		case llm.EventError:
			if ev.Err != nil {
				return "", false, ev.Err
			}
			return "", false, fmt.Errorf("user simulator provider error")
		}
	}
	out := strings.TrimSpace(text.String())
	if out == "" {
		return "", true, nil
	}
	if out == "##STOP##" || strings.Contains(out, "##STOP##") {
		return "", true, nil
	}
	return out, false, nil
}

func transcriptToString(turns []turn) string {
	lines := make([]string, 0, len(turns))
	for _, t := range turns {
		switch t.Role {
		case "user":
			lines = append(lines, "USER: "+t.Content)
		case "agent":
			lines = append(lines, "AGENT: "+t.Content)
		case "tool":
			lines = append(lines, fmt.Sprintf("(tool %s returned: %s)", t.ToolName, truncate(t.Content, 200)))
		}
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
