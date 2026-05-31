package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastschema/qjs"
)

const MaxWorkflowScriptBytes = 512 * 1024

type parsedWorkflowScript struct {
	Meta       ScriptMeta
	Executable string
}

func parseWorkflowScript(script string) (parsedWorkflowScript, error) {
	if len([]byte(script)) > MaxWorkflowScriptBytes {
		return parsedWorkflowScript{}, fmt.Errorf("workflow script exceeds %d byte limit", MaxWorkflowScriptBytes)
	}
	start := skipSpaceAndComments(script, 0)
	const prefix = "export const meta"
	if !strings.HasPrefix(script[start:], prefix) {
		return parsedWorkflowScript{}, errors.New("script must begin with export const meta = { name, description, phases }")
	}
	pos := start + len(prefix)
	pos = skipSpaceAndComments(script, pos)
	if pos >= len(script) || script[pos] != '=' {
		return parsedWorkflowScript{}, errors.New("script must begin with export const meta = { name, description, phases }")
	}
	pos = skipSpaceAndComments(script, pos+1)
	if pos >= len(script) || script[pos] != '{' {
		return parsedWorkflowScript{}, errors.New("script must begin with export const meta = { name, description, phases }")
	}
	end, err := findMatchingBrace(script, pos)
	if err != nil {
		return parsedWorkflowScript{}, err
	}
	literal := script[pos : end+1]
	if err := validatePureMetaLiteral(literal); err != nil {
		return parsedWorkflowScript{}, err
	}
	meta, err := decodeMetaLiteral(literal)
	if err != nil {
		return parsedWorkflowScript{}, err
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	if meta.Name == "" || meta.Description == "" {
		return parsedWorkflowScript{}, errors.New("meta must include name and description")
	}
	bodyStart := skipSpaceAndComments(script, end+1)
	if bodyStart < len(script) && script[bodyStart] == ';' {
		bodyStart++
	}
	executable := "const meta = " + literal + ";\nawait (async function workflowMain() {\n" + script[bodyStart:] + "\n})()"
	if err := validateWorkflowScriptBody(executable); err != nil {
		return parsedWorkflowScript{}, err
	}
	return parsedWorkflowScript{Meta: meta, Executable: executable}, nil
}

func findMatchingBrace(src string, start int) (int, error) {
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '\'', '"':
			next, err := skipQuoted(src, i)
			if err != nil {
				return 0, err
			}
			i = next
		case '`':
			return 0, errors.New("meta must be a pure literal: template strings are not allowed")
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				i = skipLineComment(src, i+2)
			} else if i+1 < len(src) && src[i+1] == '*' {
				next, err := skipBlockComment(src, i+2)
				if err != nil {
					return 0, err
				}
				i = next
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("unclosed meta literal")
}

func validatePureMetaLiteral(literal string) error {
	for i := 0; i < len(literal); i++ {
		ch := literal[i]
		switch {
		case ch == '\'' || ch == '"':
			value, next, err := readQuoted(literal, i)
			if err != nil {
				return err
			}
			after := skipSpaceAndComments(literal, next+1)
			if after < len(literal) && literal[after] == ':' && value == "constructor" {
				return errors.New("meta must be a pure literal: reserved key name not allowed in meta: constructor")
			}
			i = next
		case ch == '`':
			return errors.New("meta must be a pure literal: template strings are not allowed")
		case ch == '(' || ch == ')':
			return errors.New("meta must be a pure literal: calls and grouped expressions are not allowed")
		case strings.HasPrefix(literal[i:], "..."):
			return errors.New("meta must be a pure literal: spread is not allowed")
		case isIdentStart(rune(ch)):
			start := i
			for i+1 < len(literal) && isIdentPart(rune(literal[i+1])) {
				i++
			}
			ident := literal[start : i+1]
			after := skipSpaceAndComments(literal, i+1)
			if after < len(literal) && literal[after] == ':' {
				if ident == "constructor" {
					return errors.New("meta must be a pure literal: reserved key name not allowed in meta: constructor")
				}
				continue
			}
			switch ident {
			case "true", "false", "null":
				continue
			case "function", "new", "Date", "Math":
				return fmt.Errorf("meta must be a pure literal: %s is not allowed", ident)
			default:
				return fmt.Errorf("meta must be a pure literal: identifier %q is not allowed as a value", ident)
			}
		}
	}
	return nil
}

func decodeMetaLiteral(literal string) (ScriptMeta, error) {
	rt, err := qjs.New()
	if err != nil {
		return ScriptMeta{}, fmt.Errorf("create workflow JS runtime: %w", err)
	}
	defer rt.Close()
	val, err := rt.Eval("workflow-meta.js", qjs.Code("JSON.stringify(("+literal+"))"))
	if err != nil {
		return ScriptMeta{}, fmt.Errorf("parse meta literal: %w", err)
	}
	defer val.Free()
	var meta ScriptMeta
	if err := json.Unmarshal([]byte(val.String()), &meta); err != nil {
		return ScriptMeta{}, fmt.Errorf("decode meta literal: %w", err)
	}
	return meta, nil
}

func validateWorkflowScriptBody(script string) error {
	code := maskStringsAndComments(script)
	checks := []struct {
		name string
		pat  string
		msg  string
	}{
		{"Date.now", "Date.now(", "Workflow scripts must be deterministic: Date.now()/Math.random()/new Date() are unavailable"},
		{"Math.random", "Math.random(", "Workflow scripts must be deterministic: Date.now()/Math.random()/new Date() are unavailable"},
		{"new Date", "new Date()", "Workflow scripts must be deterministic: Date.now()/Math.random()/new Date() are unavailable"},
	}
	compact := strings.Join(strings.Fields(code), " ")
	for _, check := range checks {
		if strings.Contains(strings.ReplaceAll(compact, " ", ""), strings.ReplaceAll(check.pat, " ", "")) {
			return errors.New(check.msg)
		}
	}
	for _, word := range []string{"require", "process", "fetch", "import", "export"} {
		if containsWord(code, word) {
			return fmt.Errorf("%s is unavailable in workflow scripts", word)
		}
	}
	return nil
}

func skipSpaceAndComments(src string, pos int) int {
	for pos < len(src) {
		r, size := rune(src[pos]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(src[pos:])
		}
		if unicode.IsSpace(r) {
			pos += size
			continue
		}
		if pos+1 < len(src) && src[pos] == '/' && src[pos+1] == '/' {
			pos = skipLineComment(src, pos+2)
			if pos < len(src) {
				pos++
			}
			continue
		}
		if pos+1 < len(src) && src[pos] == '/' && src[pos+1] == '*' {
			end, err := skipBlockComment(src, pos+2)
			if err != nil {
				return pos
			}
			pos = end + 1
			continue
		}
		break
	}
	return pos
}

func skipLineComment(src string, pos int) int {
	for pos < len(src) && src[pos] != '\n' {
		pos++
	}
	return pos
}

func skipBlockComment(src string, pos int) (int, error) {
	for pos+1 < len(src) {
		if src[pos] == '*' && src[pos+1] == '/' {
			return pos + 1, nil
		}
		pos++
	}
	return 0, errors.New("unclosed block comment")
}

func skipQuoted(src string, pos int) (int, error) {
	_, next, err := readQuoted(src, pos)
	return next, err
}

func readQuoted(src string, pos int) (string, int, error) {
	quote := src[pos]
	var b strings.Builder
	for i := pos + 1; i < len(src); i++ {
		if src[i] == '\\' {
			if i+1 >= len(src) {
				return "", 0, errors.New("unterminated string literal")
			}
			b.WriteByte(src[i])
			b.WriteByte(src[i+1])
			i++
			continue
		}
		if src[i] == quote {
			return unquoteLoose(quote, b.String()), i, nil
		}
		b.WriteByte(src[i])
	}
	return "", 0, errors.New("unterminated string literal")
}

func unquoteLoose(quote byte, v string) string {
	q := string(quote)
	out, err := strconv.Unquote(q + v + q)
	if err != nil {
		return v
	}
	return out
}

func maskStringsAndComments(src string) string {
	out := []byte(src)
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case '\'', '"':
			next, err := skipQuoted(src, i)
			if err != nil {
				return string(out)
			}
			for j := i; j <= next; j++ {
				out[j] = ' '
			}
			i = next
		case '`':
			for j := i + 1; j < len(out); j++ {
				if out[j] == '`' && out[j-1] != '\\' {
					for k := i; k <= j; k++ {
						out[k] = ' '
					}
					i = j
					break
				}
			}
		case '/':
			if i+1 < len(out) && out[i+1] == '/' {
				next := skipLineComment(src, i+2)
				for j := i; j < next; j++ {
					out[j] = ' '
				}
				i = next
			} else if i+1 < len(out) && out[i+1] == '*' {
				next, err := skipBlockComment(src, i+2)
				if err != nil {
					return string(out)
				}
				for j := i; j <= next; j++ {
					out[j] = ' '
				}
				i = next
			}
		}
	}
	return string(out)
}

func containsWord(src, word string) bool {
	for i := 0; i < len(src); i++ {
		if !strings.HasPrefix(src[i:], word) {
			continue
		}
		beforeOK := i == 0 || !isIdentPart(rune(src[i-1]))
		after := i + len(word)
		afterOK := after >= len(src) || !isIdentPart(rune(src[after]))
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}
