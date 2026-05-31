package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/checkpoint"
)

const (
	maxShellSnapshotFileBytes  = 4 << 20
	maxShellSnapshotTotalBytes = 64 << 20
	maxShellSnapshotFiles      = 20000
)

var shellSnapshotSkipDirs = map[string]bool{
	".git":         true,
	".gocache":     true,
	".sessions":    true,
	".whale":       true,
	"node_modules": true,
	"__pycache__":  true,
}

type shellMutationSnapshot struct {
	root     string
	recorder checkpoint.Recorder
	files    map[string]shellSnapshotFile
}

type shellSnapshotFile struct {
	hash    [32]byte
	data    []byte
	mode    fs.FileMode
	hasData bool
}

func (b *Toolset) captureShellMutationSnapshot(ctx context.Context, root string, command string) *shellMutationSnapshot {
	recorder := checkpoint.RecorderFromContext(ctx)
	if recorder == nil {
		return nil
	}
	if shellReadOnlyCheck(map[string]any{"command": command}) {
		return nil
	}
	files := map[string]shellSnapshotFile{}
	_ = walkShellSnapshot(root, true, func(rel string, entry shellSnapshotFile) {
		files[rel] = entry
	})
	return &shellMutationSnapshot{root: root, recorder: recorder, files: files}
}

func (b *Toolset) recordShellMutations(snap *shellMutationSnapshot) {
	if snap == nil || snap.recorder == nil || snap.root == "" {
		return
	}
	after := map[string]shellSnapshotFile{}
	_ = walkShellSnapshot(snap.root, false, func(rel string, entry shellSnapshotFile) {
		after[rel] = entry
	})
	for rel, before := range snap.files {
		current, exists := after[rel]
		if exists && current.hash == before.hash && current.mode.Perm() == before.mode.Perm() {
			continue
		}
		if !before.hasData {
			continue
		}
		_ = snap.recorder.TrackFilePreimage(filepath.Join(snap.root, filepath.FromSlash(rel)), before.data, false, before.mode)
	}
	for rel := range after {
		if _, existed := snap.files[rel]; existed {
			continue
		}
		_ = snap.recorder.TrackFilePreimage(filepath.Join(snap.root, filepath.FromSlash(rel)), nil, true, 0)
	}
}

func walkShellSnapshot(root string, keepData bool, visit func(string, shellSnapshotFile)) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil
	}
	var totalBytes int64
	var files int
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shellSnapshotSkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if files >= maxShellSnapshotFiles {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.Size() > maxShellSnapshotFileBytes || (keepData && totalBytes+info.Size() > maxShellSnapshotTotalBytes) {
			visit(rel, shellSnapshotFile{mode: info.Mode()})
			files++
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		entry := shellSnapshotFile{
			hash: sha256.Sum256(data),
			mode: info.Mode(),
		}
		if keepData {
			entry.data = bytes.Clone(data)
			entry.hasData = true
			totalBytes += int64(len(data))
		}
		files++
		visit(rel, entry)
		return nil
	})
}
