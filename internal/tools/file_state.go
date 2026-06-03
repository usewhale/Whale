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

func (r *fileStateCache) delete(abs string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byAbs, filepath.Clean(abs))
}

func (r *fileStateCache) get(abs string) (fileState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.byAbs[filepath.Clean(abs)]
	return state, ok
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

func (b *Toolset) clearFileState(abs string) {
	if b.fileStates == nil {
		return
	}
	b.fileStates.delete(abs)
}

func (b *Toolset) storeFileStateFromBytes(abs string, data []byte) {
	normalized, _ := normalizeTextFileBytes(data)
	b.storeFileState(abs, normalized)
}

func (b *Toolset) validateFileState(abs, normalizedContent string) (string, string) {
	if b.fileStates == nil {
		return "read_required", "file has not been read yet; read the full file with read_file before editing"
	}
	state, ok := b.fileStates.get(abs)
	if !ok {
		return "read_required", "file has not been read yet; read the full file with read_file before editing"
	}
	if state.Hash != fileStateHash(normalizedContent) {
		return "stale_read", "file changed since it was last read or modified by Whale; read the file again before editing"
	}
	return "", ""
}
