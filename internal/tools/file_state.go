package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sync"
)

type fileState struct {
	Abs  string
	Hash string
}

type fileStateCache struct {
	mu    sync.Mutex
	byAbs map[string]fileState
}

func newFileStateCache() *fileStateCache {
	return &fileStateCache{
		byAbs: map[string]fileState{},
	}
}

func (r *fileStateCache) set(abs, normalizedContent string) fileState {
	r.mu.Lock()
	defer r.mu.Unlock()
	abs = filepath.Clean(abs)
	state := fileState{
		Abs:  abs,
		Hash: fileStateHash(normalizedContent),
	}
	r.byAbs[abs] = state
	return state
}

func fileStateHash(normalizedContent string) string {
	sum := sha256.Sum256([]byte(normalizedContent))
	return hex.EncodeToString(sum[:])
}

func (b *Toolset) storeFileState(abs, normalizedContent string) {
	if b.fileStates == nil {
		b.fileStates = newFileStateCache()
	}
	b.fileStates.set(abs, normalizedContent)
}

func (b *Toolset) storeFileStateFromBytes(abs string, data []byte) {
	normalized, _ := normalizeTextFileBytes(data)
	b.storeFileState(abs, normalized)
}
