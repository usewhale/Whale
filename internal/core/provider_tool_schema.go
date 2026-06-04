package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type ProviderToolSchemaCache struct {
	mu      sync.RWMutex
	entries map[string]map[string]any
}

type ProviderToolPayloadProvider interface {
	ProviderToolPayload() map[string]any
}

type providerToolFunctionShape struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type providerToolSchemaShape struct {
	Type     string                    `json:"type"`
	Function providerToolFunctionShape `json:"function"`
}

func NewProviderToolSchemaCache() *ProviderToolSchemaCache {
	return &ProviderToolSchemaCache{entries: map[string]map[string]any{}}
}

func ProviderToolPayload(tool Tool) map[string]any {
	if tool == nil {
		return nil
	}
	if provider, ok := tool.(ProviderToolPayloadProvider); ok {
		return provider.ProviderToolPayload()
	}
	return providerToolPayloadFromSpec(nil, DescribeTool(tool))
}

func providerToolPayloadFromSpec(cache *ProviderToolSchemaCache, spec ToolSpec) map[string]any {
	shape := providerToolShapeFromSpec(spec)
	key := providerToolShapeKey(shape)
	if cache != nil {
		if cached := cache.get(key); cached != nil {
			return cached
		}
	}
	payload := providerToolShapeToMap(shape)
	if cache != nil {
		return cache.set(key, payload)
	}
	return cloneAnyMap(payload)
}

func providerToolShapeFromSpec(spec ToolSpec) providerToolSchemaShape {
	return providerToolSchemaShape{
		Type: "function",
		Function: providerToolFunctionShape{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  FlattenSchemaForModel(spec.Parameters),
		},
	}
}

func providerToolShapeKey(shape providerToolSchemaShape) string {
	b, _ := json.Marshal(shape)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func providerToolShapeToMap(shape providerToolSchemaShape) map[string]any {
	return map[string]any{
		"type": shape.Type,
		"function": map[string]any{
			"name":        shape.Function.Name,
			"description": shape.Function.Description,
			"parameters":  cloneSchemaMap(shape.Function.Parameters),
		},
	}
}

func (c *ProviderToolSchemaCache) get(key string) map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneAnyMap(c.entries[key])
}

func (c *ProviderToolSchemaCache) set(key string, payload map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached := c.entries[key]; cached != nil {
		return cloneAnyMap(cached)
	}
	c.entries[key] = cloneAnyMap(payload)
	return cloneAnyMap(payload)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneSchemaValue(v)
	}
	return out
}
