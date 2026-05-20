package render

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmrenderer "github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
func uintPtr(v uint) *uint       { return &v }

const (
	markdownRendererPriority = 1000
	markdownSpecialChars     = "\\`*_{}[]<>()#+-.!"
)

type autolinkEscaping bool

const (
	escapeAutolinksForRenderer autolinkEscaping = true
	stripAutolinkBracketsOnly  autolinkEscaping = false
)

func Markdown(input string, width int, quiet bool) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	if width < 20 {
		// Narrow fallback skips goldmark, so only strip autolink brackets for direct terminal output.
		input = normalizeMarkdownLinks(input, stripAutolinkBracketsOnly)
		return strings.TrimRight(input, "\n")
	}
	input = normalizeMarkdownLinks(input, escapeAutolinksForRenderer)
	key := markdownCacheKey{input: input, width: width, quiet: quiet}
	if cached, ok := markdownCacheGet(key); ok {
		return cached
	}
	style := markdownStyle()
	if quiet {
		style = quietMarkdownStyle()
	}
	rendered, err := renderMarkdown(input, width, style)
	if err != nil {
		return strings.TrimRight(input, "\n")
	}
	out := strings.TrimRight(rendered, "\n")
	markdownCachePut(key, out)
	return out
}

func renderMarkdown(input string, width int, style ansi.StyleConfig) (string, error) {
	opts := ansi.Options{
		WordWrap:     width,
		ColorProfile: termenv.TrueColor,
		Styles:       style,
	}
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			extension.DefinitionList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
	ar := ansi.NewRenderer(opts)
	md.SetRenderer(gmrenderer.NewRenderer(
		gmrenderer.WithNodeRenderers(
			util.Prioritized(ar, markdownRendererPriority),
		),
	))

	var buf bytes.Buffer
	err := md.Convert([]byte(input), &buf)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func normalizeMarkdownLinks(input string, escaping autolinkEscaping) string {
	var out strings.Builder
	for i := 0; i < len(input); {
		if inFence, fenceChar, fenceLen, _ := detectFence(input, i); inFence {
			lineEnd := nextLineEnd(input, i)
			end := findFenceEnd(input, lineEnd, fenceChar, fenceLen)
			if end < 0 {
				out.WriteString(input[i:])
				return out.String()
			}
			out.WriteString(input[i:end])
			i = end
			continue
		}
		if input[i] == '`' {
			end := findInlineCodeEnd(input, i)
			out.WriteString(input[i:end])
			i = end
			continue
		}
		if repl, next, ok := parseMarkdownAutolink(input, i, escaping); ok {
			out.WriteString(repl)
			i = next
			continue
		}
		if repl, next, ok := parseMarkdownLink(input, i); ok {
			out.WriteString(repl)
			i = next
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.String()
}

func detectFence(input string, start int) (bool, byte, int, int) {
	lineStart := start == 0 || input[start-1] == '\n'
	if !lineStart {
		return false, 0, 0, 0
	}
	j := start
	for j < len(input) && j-start < 3 && input[j] == ' ' {
		j++
	}
	if j >= len(input) {
		return false, 0, 0, 0
	}
	ch := input[j]
	if ch != '`' && ch != '~' {
		return false, 0, 0, 0
	}
	k := j
	for k < len(input) && input[k] == ch {
		k++
	}
	if k-j < 3 {
		return false, 0, 0, 0
	}
	return true, ch, k - j, j - start
}

func findFenceEnd(input string, start int, fenceChar byte, fenceLen int) int {
	i := start
	for i < len(input) {
		lineEnd := nextLineEnd(input, i)
		if ok, _, runLen, _ := detectFence(input, i); ok {
			j := i
			for j < len(input) && j-i < 3 && input[j] == ' ' {
				j++
			}
			if input[j] == fenceChar && runLen >= fenceLen {
				return lineEnd
			}
		}
		if lineEnd >= len(input) {
			return -1
		}
		i = lineEnd
	}
	return -1
}

func nextLineEnd(input string, start int) int {
	nl := strings.IndexByte(input[start:], '\n')
	if nl < 0 {
		return len(input)
	}
	return start + nl + 1
}

func findInlineCodeEnd(input string, start int) int {
	run := 0
	for start+run < len(input) && input[start+run] == '`' {
		run++
	}
	i := start + run
	for i < len(input) {
		if input[i] == '`' {
			j := i
			for j < len(input) && input[j] == '`' {
				j++
			}
			if j-i == run {
				return j
			}
			i = j
			continue
		}
		i++
	}
	return len(input)
}

func parseMarkdownAutolink(input string, start int, escaping autolinkEscaping) (string, int, bool) {
	if start >= len(input) || input[start] != '<' {
		return "", start, false
	}
	end := strings.IndexByte(input[start+1:], '>')
	if end < 0 {
		return "", start, false
	}
	end += start + 1
	target := input[start+1 : end]
	if target == "" || strings.ContainsAny(target, " \t\r\n") || !isMarkdownAutolinkTarget(target) {
		return "", start, false
	}
	if escaping == stripAutolinkBracketsOnly {
		return target, end + 1, true
	}
	return escapeMarkdownLiteral(target), end + 1, true
}

func isMarkdownAutolinkTarget(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

func escapeMarkdownLiteral(input string) string {
	var out strings.Builder
	for i := 0; i < len(input); i++ {
		if strings.ContainsRune(markdownSpecialChars, rune(input[i])) {
			out.WriteByte('\\')
		}
		out.WriteByte(input[i])
	}
	return out.String()
}

func parseMarkdownLink(input string, start int) (string, int, bool) {
	if start >= len(input) || input[start] != '[' {
		return "", start, false
	}
	if start > 0 && input[start-1] == '!' {
		return "", start, false
	}
	endText := -1
	depth := 0
	for i := start; i < len(input); i++ {
		switch input[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				endText = i
				goto foundText
			}
		}
	}
foundText:
	if endText < 0 || endText+1 >= len(input) || input[endText+1] != '(' {
		return "", start, false
	}
	endURL := -1
	depth = 0
	for i := endText + 1; i < len(input); i++ {
		switch input[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				endURL = i
				goto foundURL
			}
		}
	}
foundURL:
	if endURL < 0 {
		return "", start, false
	}
	label := input[start+1 : endText]
	target := input[endText+2 : endURL]
	if label == target {
		return target, endURL + 1, true
	}
	return label + " (" + target + ")", endURL + 1, true
}

func markdownStyle() ansi.StyleConfig {
	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{BlockPrefix: "", BlockSuffix: ""},
			Margin:         uintPtr(0),
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Bold: boolPtr(true)},
		},
		Strong: ansi.StylePrimitive{Bold: boolPtr(true)},
		Emph:   ansi.StylePrimitive{Italic: boolPtr(true)},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "│ ",
				Italic: boolPtr(true),
			},
		},
		List: ansi.StyleList{
			LevelIndent: 2,
			StyleBlock: ansi.StyleBlock{
				IndentToken: stringPtr(" "),
			},
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Prefix: "  ",
				},
				Margin: uintPtr(1),
			},
		},
		HorizontalRule: ansi.StylePrimitive{
			Format: "────────────────────────────",
		},
		Task: ansi.StyleTask{
			Ticked:   "[x] ",
			Unticked: "[ ] ",
		},
	}
}

func quietMarkdownStyle() ansi.StyleConfig {
	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{BlockPrefix: "", BlockSuffix: ""},
			Margin:         uintPtr(0),
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "# "},
		},
		List: ansi.StyleList{
			LevelIndent: 2,
			StyleBlock: ansi.StyleBlock{
				IndentToken: stringPtr(" "),
			},
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "- ",
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "> "},
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "`", Suffix: "`"},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{Prefix: "  "},
				Margin:         uintPtr(1),
			},
		},
		Link: ansi.StylePrimitive{
			Format: "{{.text}}",
		},
		Task: ansi.StyleTask{
			Ticked:   "[x] ",
			Unticked: "[ ] ",
		},
	}
}
