package shellrisk

import (
	"strings"
	"unicode"
)

func safeReadDecision(reason, key string) Decision {
	return Decision{
		Allow:        true,
		Code:         CodeSafeRead,
		Level:        LevelSafeRead,
		Reason:       reason,
		ApprovalKeys: []string{key},
		SessionScope: "this safe shell command family",
	}
}
func boundedWriteDecision(reason, key, writeScope string) Decision {
	return Decision{
		Code:         CodeBoundedWrite,
		Level:        LevelBoundedWrite,
		Reason:       reason,
		ApprovalKeys: []string{key},
		SessionScope: "this bounded shell command family",
		WriteScopes:  []string{writeScope},
	}
}
func isCommandName(v string) bool {
	if strings.TrimSpace(v) == "" || strings.Contains(v, "/") || strings.HasPrefix(v, "-") {
		return false
	}
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '_', '.', '-', '+':
			continue
		default:
			return false
		}
	}
	return true
}
func lowerArgv(argv []string) []string {
	lower := make([]string, 0, len(argv))
	for _, arg := range argv {
		lower = append(lower, strings.ToLower(arg))
	}
	return lower
}
func argvHasPrefix(argv []string, prefix string) bool {
	prefixArgv := strings.Fields(strings.ToLower(strings.TrimSpace(prefix)))
	if len(argv) < len(prefixArgv) {
		return false
	}
	for i, want := range prefixArgv {
		if argv[i] != want {
			return false
		}
	}
	return true
}
func semanticKey(kind string, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	switch argv[0] {
	case "git":
		if len(argv) >= 2 {
			return "shell:" + kind + ":git:" + argv[1]
		}
	case "python", "python3":
		if len(argv) >= 3 && argv[1] == "-m" {
			return "shell:" + kind + ":" + argv[0] + ":-m-" + argv[2]
		}
	case "npx":
		if len(argv) >= 2 {
			return "shell:" + kind + ":npx:" + argv[1]
		}
	default:
		return "shell:" + kind + ":" + argv[0]
	}
	return "shell:" + kind + ":" + argv[0]
}
