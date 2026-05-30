package tools

import "github.com/usewhale/whale/internal/core"

func (b *Toolset) webTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "web_search",
			description: "Search the public web and return structured results. Uses DuckDuckGo HTML with Bing fallback when needed; blocked, timed out, or unparseable searches return recovery hints.",
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
			description: "Fetch a URL, extract readable content, and answer the supplied prompt. Use official/raw URLs when possible; recovery hints are returned for blocked, timed out, or authenticated resources.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Target URL (http/https). Plain http URLs are upgraded to https except localhost/IPs."},
					"prompt":     map[string]any{"type": "string", "description": "What to extract or answer from the fetched content."},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				},
				"required": []string{"url", "prompt"},
			},
			readOnly: true,
			fn:       b.fetch,
		},
		toolFn{
			name:        "web_fetch",
			description: "Fetch a web page, extract readable content plus page title, and answer the supplied prompt. Use official/raw URLs when possible; recovery hints are returned for blocked, timed out, or authenticated resources.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Target URL (http/https). Plain http URLs are upgraded to https except localhost/IPs."},
					"prompt":     map[string]any{"type": "string", "description": "What to extract or answer from the fetched content."},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				},
				"required": []string{"url", "prompt"},
			},
			readOnly: true,
			fn:       b.webFetch,
		},
	}
}
