package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/telemetry"
)

const cacheShapeTailMessages = 8

const (
	cacheShapeRequestAgent        = "agent"
	cacheShapeRequestCompact      = "compact"
	cacheShapeRequestForceSummary = "force_summary"
	cacheShapeRequestSideQuestion = "side_question"
)

type cacheShapeMessage struct {
	Role             core.Role            `json:"role"`
	Text             string               `json:"text,omitempty"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
	ToolCallID       string               `json:"tool_call_id,omitempty"`
	ToolCalls        []cacheShapeToolCall `json:"tool_calls,omitempty"`
}

type cacheShapeToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type pendingCacheShapeToolCall struct {
	ID                   string
	ConsumesStoredResult bool
}

func buildCacheShape(history []core.Message, tools []core.Tool, assistantPrefix string) *telemetry.CacheShape {
	return buildCacheShapeWithSystemBlocks(history, tools, assistantPrefix, nil)
}

func buildCacheShapeWithSystemBlocks(history []core.Message, tools []core.Tool, assistantPrefix string, systemBlocks []string) *telemetry.CacheShape {
	return buildCacheShapeForRequest(cacheShapeRequestAgent, history, tools, assistantPrefix, systemBlocks)
}

func buildCacheShapeForRequest(requestKind string, history []core.Message, tools []core.Tool, assistantPrefix string, systemBlocks []string) *telemetry.CacheShape {
	return buildCacheShapeForRequestWithRuntime(requestKind, history, tools, assistantPrefix, systemBlocks, nil)
}

func buildCacheShapeForRequestWithRuntime(requestKind string, history []core.Message, tools []core.Tool, assistantPrefix string, systemBlocks, runtimeBlocks []string) *telemetry.CacheShape {
	var system []cacheShapeMessage
	var log []cacheShapeMessage
	var pendingToolCalls []pendingCacheShapeToolCall
	providerMessageIndex := 0
	flushPending := func() []cacheShapeMessage {
		if len(pendingToolCalls) == 0 {
			return nil
		}
		out := make([]cacheShapeMessage, 0, len(pendingToolCalls))
		for _, pending := range pendingToolCalls {
			out = append(out, syntheticMissingToolResultShape(pending.ID))
		}
		pendingToolCalls = nil
		return out
	}
	for _, msg := range history {
		shaped := shapeMessage(msg, &pendingToolCalls, providerMessageIndex, flushPending)
		if len(shaped) == 0 {
			continue
		}
		if msg.Role == core.RoleSystem {
			system = append(system, shaped...)
		} else {
			log = append(log, shaped...)
		}
		providerMessageIndex += len(shaped)
	}
	if flushed := flushPending(); len(flushed) > 0 {
		log = append(log, flushed...)
		providerMessageIndex += len(flushed)
	}

	tailLen := min(cacheShapeTailMessages, len(log))
	head := log[:len(log)-tailLen]
	tail := log[len(log)-tailLen:]
	toolPayload := shapeToolPayload(tools)
	systemForShape := system
	if len(systemBlocks) > 0 {
		systemForShape = systemBlocksToShapeMessages(systemBlocks)
	}
	shape := &telemetry.CacheShape{
		RequestKind:    strings.TrimSpace(requestKind),
		SystemHash:     hashJSON(systemForShape),
		SystemSegments: shapeSystemSegments(systemBlocks, system),
		SystemBytes:    stableJSONBytes(systemForShape),
		ToolsHash:      hashJSON(toolPayload),
		ToolSegments:   shapeToolSegments(toolPayload),
		ToolsBytes:     stableJSONBytes(toolPayload),
		LogMessages:    len(log),
		TailMessages:   tailLen,
	}
	if len(runtimeBlocks) > 0 {
		runtimeSystem := systemBlocksToShapeMessages(runtimeBlocks)
		shape.RuntimeHash = hashJSON(runtimeSystem)
		shape.RuntimeSegments = shapeSystemSegments(runtimeBlocks, runtimeSystem)
		shape.RuntimeBytes = stableJSONBytes(runtimeSystem)
	}
	if strings.TrimSpace(assistantPrefix) != "" {
		shape.AssistantPrefixHash = hashJSON(assistantPrefix)
		shape.AssistantPrefixBytes = len([]byte(assistantPrefix))
	}
	if len(head) > 0 {
		shape.LogHeadHash = hashJSON(head)
		shape.LogHeadBytes = stableJSONBytes(head)
	}
	if len(tail) > 0 {
		shape.LogTailHash = hashJSON(tail)
		shape.LogTailBytes = stableJSONBytes(tail)
	}
	shape.PrefixHash = hashJSON(struct {
		SystemHash  string `json:"system_hash,omitempty"`
		RuntimeHash string `json:"runtime_hash,omitempty"`
		ToolsHash   string `json:"tools_hash,omitempty"`
		FewShotHash string `json:"fewshot_hash,omitempty"`
	}{
		SystemHash:  shape.SystemHash,
		RuntimeHash: shape.RuntimeHash,
		ToolsHash:   shape.ToolsHash,
		FewShotHash: shape.FewShotHash,
	})
	shape.PrefixBytes = shape.SystemBytes + shape.RuntimeBytes + shape.ToolsBytes
	shape.RequestHash = hashJSON(struct {
		RequestKind         string `json:"request_kind,omitempty"`
		PrefixHash          string `json:"prefix_hash,omitempty"`
		SystemHash          string `json:"system_hash,omitempty"`
		RuntimeHash         string `json:"runtime_hash,omitempty"`
		ToolsHash           string `json:"tools_hash,omitempty"`
		FewShotHash         string `json:"fewshot_hash,omitempty"`
		AssistantPrefixHash string `json:"assistant_prefix_hash,omitempty"`
		LogHeadHash         string `json:"log_head_hash,omitempty"`
		LogTailHash         string `json:"log_tail_hash,omitempty"`
		LogMessages         int    `json:"log_messages,omitempty"`
		TailMessages        int    `json:"tail_messages,omitempty"`
	}{
		RequestKind:         shape.RequestKind,
		PrefixHash:          shape.PrefixHash,
		SystemHash:          shape.SystemHash,
		RuntimeHash:         shape.RuntimeHash,
		ToolsHash:           shape.ToolsHash,
		FewShotHash:         shape.FewShotHash,
		AssistantPrefixHash: shape.AssistantPrefixHash,
		LogHeadHash:         shape.LogHeadHash,
		LogTailHash:         shape.LogTailHash,
		LogMessages:         shape.LogMessages,
		TailMessages:        shape.TailMessages,
	})
	return shape
}

func systemBlocksToShapeMessages(blocks []string) []cacheShapeMessage {
	out := make([]cacheShapeMessage, 0, len(blocks))
	for _, block := range blocks {
		if trimmed := strings.TrimSpace(block); trimmed != "" {
			out = append(out, cacheShapeMessage{Role: core.RoleSystem, Text: trimmed})
		}
	}
	return out
}

func shapeToolSegments(tools []map[string]any) []telemetry.CacheShapeSegment {
	if len(tools) == 0 {
		return nil
	}
	out := make([]telemetry.CacheShapeSegment, 0, len(tools))
	for i, tool := range tools {
		out = append(out, telemetry.CacheShapeSegment{
			Index:     i,
			Name:      shapeToolName(tool),
			Stability: "immutable",
			Hash:      hashJSON(tool),
			Bytes:     stableJSONBytes(tool),
		})
	}
	return out
}

func shapeToolName(tool map[string]any) string {
	fn, _ := tool["function"].(map[string]any)
	name, _ := fn["name"].(string)
	return name
}

func shapeSystemSegments(systemBlocks []string, system []cacheShapeMessage) []telemetry.CacheShapeSegment {
	if len(systemBlocks) == 0 {
		systemBlocks = systemMessagesToBlocks(system)
	}
	out := make([]telemetry.CacheShapeSegment, 0, len(systemBlocks))
	for i, block := range systemBlocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		name, stability := classifySystemBlock(trimmed, i)
		out = append(out, telemetry.CacheShapeSegment{
			Index:     i,
			Name:      name,
			Stability: stability,
			Hash:      hashJSON(cacheShapeMessage{Role: core.RoleSystem, Text: trimmed}),
			Bytes:     len([]byte(trimmed)),
		})
	}
	return out
}

func systemMessagesToBlocks(system []cacheShapeMessage) []string {
	if len(system) == 0 {
		return nil
	}
	out := make([]string, 0, len(system))
	for _, msg := range system {
		if strings.TrimSpace(msg.Text) != "" {
			out = append(out, msg.Text)
		}
	}
	return out
}

func classifySystemBlock(block string, index int) (string, string) {
	lower := strings.ToLower(block)
	switch {
	case strings.HasPrefix(block, "Current Whale runtime:"):
		return "runtime_context", "dynamic"
	case strings.HasPrefix(block, "Available skills"):
		return "skills", "dynamic"
	case strings.HasPrefix(block, "# Project Memory"):
		return "project_memory", "dynamic"
	case strings.Contains(lower, "current whale workspace root") ||
		strings.Contains(lower, "current worktree root") ||
		strings.Contains(lower, "original workspace"):
		return "workspace_context", "dynamic"
	case strings.HasPrefix(block, "Tool use policy."):
		return "tool_policy", "immutable"
	case strings.HasPrefix(block, "Workflow authoring."):
		return "workflow_authoring", "immutable"
	case strings.HasPrefix(block, "Mode contract."):
		return "mode_contract", "immutable"
	case strings.HasPrefix(block, "Focus view is active"):
		return "output_style", "dynamic"
	case strings.HasPrefix(block, "Mode switching commands"):
		return "mode_switching", "immutable"
	case strings.HasPrefix(block, "Delegation policy"):
		return "delegation_policy", "immutable"
	case strings.HasPrefix(block, "For questions about the current date or time"):
		return "date_time_policy", "immutable"
	case strings.HasPrefix(block, "For branch decisions"):
		return "decision_policy", "immutable"
	default:
		return fmt.Sprintf("system_block_%02d", index), "immutable"
	}
}

func shapeMessage(msg core.Message, pendingToolCalls *[]pendingCacheShapeToolCall, providerMessageIndex int, flushPending func() []cacheShapeMessage) []cacheShapeMessage {
	switch msg.Role {
	case core.RoleSystem:
		out := flushPending()
		return append(out, cacheShapeMessage{Role: core.RoleSystem, Text: msg.Text})
	case core.RoleUser:
		out := flushPending()
		return append(out, cacheShapeMessage{Role: core.RoleUser, Text: msg.Text})
	case core.RoleAssistant:
		out := flushPending()
		shaped := cacheShapeMessage{
			Role:      core.RoleAssistant,
			Text:      msg.Text,
			ToolCalls: shapeToolCalls(msg.ToolCalls, providerMessageIndex),
		}
		if len(msg.ToolCalls) > 0 {
			shaped.ReasoningContent = msg.Reasoning
		}
		for callIdx, tc := range msg.ToolCalls {
			*pendingToolCalls = append(*pendingToolCalls, pendingCacheShapeToolCall{
				ID:                   cacheShapeToolCallID(tc.ID, providerMessageIndex, callIdx),
				ConsumesStoredResult: strings.TrimSpace(tc.ID) != "",
			})
		}
		return append(out, shaped)
	case core.RoleTool:
		return shapeToolResults(msg.ToolResults, pendingToolCalls)
	default:
		return nil
	}
}

func shapeToolCalls(calls []core.ToolCall, providerMessageIndex int) []cacheShapeToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]cacheShapeToolCall, 0, len(calls))
	for callIdx, call := range calls {
		out = append(out, cacheShapeToolCall{
			ID:        cacheShapeToolCallID(call.ID, providerMessageIndex, callIdx),
			Name:      call.Name,
			Arguments: call.Input,
		})
	}
	return out
}

func cacheShapeToolCallID(id string, providerMessageIndex, callIdx int) string {
	if strings.TrimSpace(id) == "" {
		return fmt.Sprintf("whale_synthetic_call_%d_%d", providerMessageIndex, callIdx)
	}
	return id
}

func shapeToolResults(results []core.ToolResult, pendingToolCalls *[]pendingCacheShapeToolCall) []cacheShapeMessage {
	if len(results) == 0 || len(*pendingToolCalls) == 0 {
		return nil
	}
	out := make([]cacheShapeMessage, 0, len(results))
	for _, result := range results {
		for len(*pendingToolCalls) > 0 && !(*pendingToolCalls)[0].ConsumesStoredResult {
			out = append(out, syntheticMissingToolResultShape((*pendingToolCalls)[0].ID))
			*pendingToolCalls = (*pendingToolCalls)[1:]
		}
		if len(*pendingToolCalls) == 0 || strings.TrimSpace(result.ToolCallID) == "" {
			continue
		}
		match := -1
		for i, pending := range *pendingToolCalls {
			if pending.ConsumesStoredResult && pending.ID == result.ToolCallID {
				match = i
				break
			}
		}
		if match < 0 {
			continue
		}
		for i := 0; i < match; i++ {
			out = append(out, syntheticMissingToolResultShape((*pendingToolCalls)[i].ID))
		}
		id := (*pendingToolCalls)[match].ID
		out = append(out, cacheShapeMessage{
			Role:       core.RoleTool,
			ToolCallID: id,
			Text:       compact.ToolResultReplayContent(core.ToolResultModelText(result)),
		})
		*pendingToolCalls = (*pendingToolCalls)[match+1:]
	}
	return out
}

func syntheticMissingToolResultShape(id string) cacheShapeMessage {
	return cacheShapeMessage{
		Role:       core.RoleTool,
		ToolCallID: id,
		Text:       `{"success":false,"error":"missing tool result recovered before provider send","code":"missing_tool_result_recovered"}`,
	}
}

func shapeToolPayload(tools []core.Tool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if payload := core.ProviderToolPayload(tool); payload != nil {
			out = append(out, payload)
		}
	}
	return out
}

func stableJSONBytes(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}
