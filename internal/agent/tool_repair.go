package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

var (
	trailingCommaBeforeCloser = regexp.MustCompile(`,(\s*[}\]])`)
	truncatedJSONColonEnding  = regexp.MustCompile(`"\s*:\s*$`)
	topLevelJSONPair          = regexp.MustCompile(`"([A-Za-z0-9_\-\.]+)"\s*:\s*("([^"\\]|\\.)*"|-?\d+(\.\d+)?|true|false|null)`)
)

type stormConfig struct {
	WindowSize int
	Threshold  int
}

func defaultStormConfig() stormConfig {
	return stormConfig{WindowSize: 6, Threshold: 3}
}

type truncationRepairResult struct {
	repaired string
	changed  bool
	notes    []string
}

type repairReport struct {
	scavenged        int
	truncationsFixed int
	stormsBroken     int
	notes            []string
	repairedCalls    []core.ToolCall
}

type toolCallRepair struct {
	storm       *stormBreaker
	maxScavenge int
}

func newToolCallRepair(cfg stormConfig) *toolCallRepair {
	window := cfg.WindowSize
	if window <= 0 {
		window = 6
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 3
	}
	return &toolCallRepair{
		storm:       &stormBreaker{windowSize: window, threshold: threshold},
		maxScavenge: 4,
	}
}

func (r *toolCallRepair) resetStorm() {
	if r == nil || r.storm == nil {
		return
	}
	r.storm.reset()
}

func (r *toolCallRepair) process(
	declared []core.ToolCall,
	reasoning, content string,
	allowed map[string]bool,
	isMutating func(core.ToolCall) bool,
) ([]core.ToolCall, []core.ToolResult, repairReport) {
	rep := repairReport{}
	out := make([]core.ToolCall, 0, len(declared))
	out = append(out, declared...)
	sc, count := scavengeToolCalls(reasoning, content, allowed, out)
	if count > 0 {
		out = append(out, sc...)
		rep.scavenged = count
		rep.notes = append(rep.notes, fmt.Sprintf("scavenged:%d", count))
	}
	for i := range out {
		res := repairTruncatedJSON(out[i].Input)
		if res.changed {
			out[i].Input = res.repaired
			rep.truncationsFixed++
			rep.repairedCalls = append(rep.repairedCalls, out[i])
			rep.notes = append(rep.notes, res.notes...)
		}
	}
	kept := make([]core.ToolCall, 0, len(out))
	dropped := make([]core.ToolResult, 0)
	for _, c := range out {
		verdict := r.storm.inspect(c, isMutating)
		if verdict.suppress {
			rep.stormsBroken++
			rep.notes = append(rep.notes, verdict.reason)
			dropped = append(dropped, core.ToolResult{
				ToolCallID: c.ID,
				Name:       c.Name,
				Content:    `{"ok":false,"error":"repetitive tool call blocked","code":"storm_blocked"}`,
				IsError:    true,
			})
			continue
		}
		kept = append(kept, c)
	}
	return kept, dropped, rep
}

func repairTruncatedJSON(raw string) truncationRepairResult {
	if strings.TrimSpace(raw) == "" {
		return truncationRepairResult{repaired: "{}", changed: true, notes: []string{"empty input -> {}"}}
	}
	if json.Valid([]byte(raw)) {
		return truncationRepairResult{repaired: raw, changed: false}
	}
	candidate := raw
	if s, ok := closeLikelyJSON(candidate); ok {
		candidate = s
	}
	if truncatedJSONColonEnding.MatchString(candidate) {
		candidate += " null"
	}
	candidate = trailingCommaBeforeCloser.ReplaceAllString(candidate, "$1")
	if json.Valid([]byte(candidate)) {
		return truncationRepairResult{repaired: candidate, changed: true, notes: []string{"truncation repaired"}}
	}
	// Salvage minimal object from recoverable prefix before hard fallback.
	if obj, ok := salvageJSONObjectPrefix(candidate); ok {
		return truncationRepairResult{repaired: obj, changed: true, notes: []string{"salvaged prefix object"}}
	}
	// If we can extract at least one k:v pair, preserve it instead of dropping all args.
	if kv, ok := salvageTopLevelPairs(candidate); ok {
		return truncationRepairResult{repaired: kv, changed: true, notes: []string{"salvaged top-level pairs"}}
	}
	return truncationRepairResult{repaired: "{}", changed: true, notes: []string{"fallback -> {}"}}
}

func closeLikelyJSON(raw string) (string, bool) {
	var (
		stack    []byte
		inString bool
		escape   bool
	)

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			n := len(stack)
			if n == 0 || stack[n-1] != ch {
				return raw, false
			}
			stack = stack[:n-1]
		}
	}

	var b strings.Builder
	b.WriteString(raw)
	if inString {
		b.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i])
	}
	return b.String(), true
}

func scavengeToolCalls(reasoning, content string, allowed map[string]bool, existing []core.ToolCall) ([]core.ToolCall, int) {
	text := reasoning + "\n" + content
	if strings.TrimSpace(text) == "" {
		return nil, 0
	}
	signatures := make(map[string]bool, len(existing))
	for _, c := range existing {
		signatures[c.Name+"\n"+strings.TrimSpace(c.Input)] = true
	}

	found := 0
	out := make([]core.ToolCall, 0)
	maxCalls := 4
	for _, obj := range extractJSONObjectCandidates(text) {
		if found >= maxCalls {
			break
		}
		payloadName, payloadInput, ok := parseScavengedToolCall(obj)
		if !ok {
			continue
		}
		name := strings.TrimSpace(payloadName)
		if name == "" || !allowed[name] {
			continue
		}
		input := payloadInput
		sig := name + "\n" + strings.TrimSpace(input)
		if signatures[sig] {
			continue
		}
		found++
		signatures[sig] = true
		out = append(out, core.ToolCall{
			ID:    fmt.Sprintf("scavenge-%d", found),
			Name:  name,
			Input: input,
		})
	}
	return out, found
}

func parseScavengedToolCall(obj string) (name string, input string, ok bool) {
	// shape A: {"name":"tool","arguments":{...}}
	if name, input, ok := parseNamedScavengedToolCall(obj); ok {
		return name, input, true
	}
	// shape B: {"function":{"name":"tool","arguments":{...}}}
	if name, input, ok := parseFunctionScavengedToolCall(obj); ok {
		return name, input, true
	}
	// shape C: {"function_call":{"name":"tool","arguments":"{...json...}"}}
	if name, input, ok := parseFunctionCallScavengedToolCall(obj); ok {
		return name, input, true
	}
	return "", "", false
}

func parseNamedScavengedToolCall(obj string) (name string, input string, ok bool) {
	var a struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(obj), &a); err == nil && strings.TrimSpace(a.Name) != "" {
		return marshalScavengedArgs(a.Name, a.Arguments)
	}
	return "", "", false
}

func parseFunctionScavengedToolCall(obj string) (name string, input string, ok bool) {
	var b struct {
		Function struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(obj), &b); err == nil && strings.TrimSpace(b.Function.Name) != "" {
		return marshalScavengedArgs(b.Function.Name, b.Function.Arguments)
	}
	return "", "", false
}

func parseFunctionCallScavengedToolCall(obj string) (name string, input string, ok bool) {
	var c struct {
		FunctionCall struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		} `json:"function_call"`
	}
	if err := json.Unmarshal([]byte(obj), &c); err == nil && strings.TrimSpace(c.FunctionCall.Name) != "" {
		return marshalScavengedArgs(c.FunctionCall.Name, c.FunctionCall.Arguments)
	}
	return "", "", false
}

func marshalScavengedArgs(name string, args any) (string, string, bool) {
	b, err := marshalArgsPayload(args)
	if err != nil {
		return "", "", false
	}
	return name, string(b), true
}

func marshalArgsPayload(v any) ([]byte, error) {
	switch tv := v.(type) {
	case nil:
		return []byte("{}"), nil
	case string:
		s := strings.TrimSpace(tv)
		if s == "" {
			return []byte("{}"), nil
		}
		if json.Valid([]byte(s)) {
			return []byte(s), nil
		}
		return json.Marshal(map[string]any{"input": s})
	default:
		return json.Marshal(v)
	}
}

func salvageJSONObjectPrefix(raw string) (string, bool) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return "", false
	}
	for end := len(raw); end > start; end-- {
		candidate := strings.TrimSpace(raw[start:end])
		if !strings.HasPrefix(candidate, "{") {
			continue
		}
		closed, ok := closeLikelyJSON(candidate)
		if !ok {
			continue
		}
		closed = trailingCommaBeforeCloser.ReplaceAllString(closed, "$1")
		if json.Valid([]byte(closed)) {
			return closed, true
		}
	}
	return "", false
}

func salvageTopLevelPairs(raw string) (string, bool) {
	r := strings.TrimSpace(raw)
	if r == "" {
		return "", false
	}
	start := strings.Index(r, "{")
	if start >= 0 {
		r = r[start+1:]
	}
	matches := topLevelJSONPair.FindAllStringSubmatch(r, 12)
	if len(matches) == 0 {
		return "", false
	}
	out := map[string]any{}
	for _, m := range matches {
		k := m[1]
		lit := m[2]
		var v any
		if err := json.Unmarshal([]byte(lit), &v); err != nil {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return "", false
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", false
	}
	return string(b), true
}

type stormVerdict struct {
	suppress bool
	reason   string
}

type stormEntry struct {
	name     string
	args     string
	readOnly bool
}

type stormBreaker struct {
	windowSize int
	threshold  int
	recent     []stormEntry
}

func (s *stormBreaker) reset() { s.recent = s.recent[:0] }

func (s *stormBreaker) inspect(call core.ToolCall, isMutating func(core.ToolCall) bool) stormVerdict {
	mut := false
	if isMutating != nil {
		mut = isMutating(call)
	}
	if mut {
		kept := s.recent[:0]
		for _, e := range s.recent {
			if !e.readOnly {
				kept = append(kept, e)
			}
		}
		s.recent = kept
	}
	args := strings.TrimSpace(call.Input)
	count := 0
	for _, e := range s.recent {
		if e.name == call.Name && e.args == args {
			count++
		}
	}
	if count >= s.threshold {
		return stormVerdict{
			suppress: true,
			reason:   fmt.Sprintf("call-storm suppressed: %s called with identical args %d times within window=%d", call.Name, count+1, s.windowSize),
		}
	}
	s.recent = append(s.recent, stormEntry{name: call.Name, args: args, readOnly: !mut})
	if len(s.recent) > s.windowSize {
		s.recent = s.recent[len(s.recent)-s.windowSize:]
	}
	return stormVerdict{}
}

func extractJSONObjectCandidates(text string) []string {
	const maxLen = 8192
	out := make([]string, 0, 8)
	start := -1
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				if i-start+1 <= maxLen {
					out = append(out, text[start:i+1])
				}
				start = -1
			}
		}
	}
	return out
}
