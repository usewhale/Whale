package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type fileSnapshot struct {
	ID   string
	Abs  string
	Hash string
}

type fileSnapshotRegistry struct {
	mu   sync.Mutex
	next uint64
	byID map[string]fileSnapshot
}

func newFileSnapshotRegistry() *fileSnapshotRegistry {
	return &fileSnapshotRegistry{
		byID: map[string]fileSnapshot{},
	}
}

func (r *fileSnapshotRegistry) create(abs, normalizedContent string) fileSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	abs = filepath.Clean(abs)
	return fileSnapshot{
		ID:   fmt.Sprintf("fs-%016x", r.next),
		Abs:  abs,
		Hash: fileSnapshotHash(normalizedContent),
	}
}

func (r *fileSnapshotRegistry) store(snapshot fileSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[snapshot.ID] = snapshot
}

func (r *fileSnapshotRegistry) get(id string) (fileSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, ok := r.byID[strings.TrimSpace(id)]
	return snapshot, ok
}

func fileSnapshotHash(normalizedContent string) string {
	sum := sha256.Sum256([]byte(normalizedContent))
	return hex.EncodeToString(sum[:])
}

func (b *Toolset) createFileSnapshot(abs, normalizedContent string) fileSnapshot {
	if b.fileSnapshots == nil {
		b.fileSnapshots = newFileSnapshotRegistry()
	}
	return b.fileSnapshots.create(abs, normalizedContent)
}

func (b *Toolset) storeFileSnapshot(snapshot fileSnapshot) {
	if b.fileSnapshots == nil {
		b.fileSnapshots = newFileSnapshotRegistry()
	}
	b.fileSnapshots.store(snapshot)
}

func (b *Toolset) validateFileSnapshot(abs, snapshotID, normalizedContent string) (string, string) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return "read_required", "edit requires snapshot_id from a prior full read_file result for this file"
	}
	if b.fileSnapshots == nil {
		return "read_required", "snapshot_id is not valid in this session; read the file again with read_file before editing"
	}
	snapshot, ok := b.fileSnapshots.get(snapshotID)
	if !ok {
		return "read_required", "snapshot_id is not valid in this session; read the file again with read_file before editing"
	}
	abs = filepath.Clean(abs)
	if snapshot.Abs != abs {
		return "snapshot_mismatch", fmt.Sprintf("snapshot_id belongs to %s, not %s; read the target file again before editing", b.displayPath(snapshot.Abs), b.displayPath(abs))
	}
	if snapshot.Hash != fileSnapshotHash(normalizedContent) {
		return "stale_read", "file changed after the snapshot was created; read the file again before editing"
	}
	return "", ""
}
