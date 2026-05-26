package tools

import "github.com/usewhale/whale/internal/core"

func (b *Toolset) webTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "web_search",
			description: "Search the public web and return structured results. Uses DuckDuckGo HTML with Bing fallback when needed.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query"},
					"q":     map[string]any{"type": "string", "description": "Alias for query"},
					"search_query": map[string]any{
						"type":        "array",
						"description": "Compatibility format: [{\"q\":\"...\", \"max_results\": 5}]",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"q":           map[string]any{"type": "string"},
								"query":       map[string]any{"type": "string"},
								"max_results": map[string]any{"type": "integer"},
							},
						},
					},
					"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
					"timeout_ms":  map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				},
			},
			readOnly: true,
			fn:       b.webSearch,
		},
		toolFn{
			name:        "fetch",
			description: "Fetch a URL and return content. Supports text|markdown|html output formats with aliases and timeout/truncation control.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Target URL (http/https)"},
					"format":     map[string]any{"type": "string", "enum": []string{"text", "txt", "plain", "markdown", "md", "html", "raw", "bytes"}},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				},
				"required": []string{"url"},
			},
			readOnly: true,
			fn:       b.fetch,
		},
		toolFn{
			name:        "web_fetch",
			description: "Fetch a web page and extract readable content plus page title. Supports text|markdown|html output formats with aliases.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Target URL (http/https)"},
					"format":     map[string]any{"type": "string", "enum": []string{"text", "txt", "plain", "markdown", "md", "html", "raw", "bytes"}},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				},
				"required": []string{"url"},
			},
			readOnly: true,
			fn:       b.webFetch,
		},
	}
}
