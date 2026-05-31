package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/usewhale/whale/internal/checkpoint"
)

type fileMutationLocks struct {
	stripes [64]sync.Mutex
}

func newFileMutationLocks() *fileMutationLocks {
	return &fileMutationLocks{}
}

func (l *fileMutationLocks) lock(paths []string) func() {
	if l == nil {
		return func() {}
	}
	indexes := uniqueSortedLockIndexes(paths, len(l.stripes))
	held := make([]int, 0, len(indexes))
	for _, index := range indexes {
		l.stripes[index].Lock()
		held = append(held, index)
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			l.stripes[held[i]].Unlock()
		}
	}
}

func uniqueSortedLockIndexes(paths []string, stripes int) []int {
	seen := make(map[int]bool, len(paths))
	out := make([]int, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "" {
			continue
		}
		index := int(fnv32(path) % uint32(stripes))
		if seen[index] {
			continue
		}
		seen[index] = true
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

func fnv32(s string) uint32 {
	hash := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		hash ^= uint32(s[i])
		hash *= 16777619
	}
	return hash
}

type fileCommitPlan struct {
	path           string
	abs            string
	expectedBytes  []byte
	expectedExists bool
	afterBytes     []byte
	remove         bool
}

func (b *Toolset) commitFilePlans(ctx context.Context, plans []fileCommitPlan) error {
	paths := make([]string, 0, len(plans))
	for _, plan := range plans {
		paths = append(paths, plan.abs)
	}
	unlock := b.fileLocks.lock(paths)
	defer unlock()

	if b.beforeFileCommit != nil {
		for _, plan := range plans {
			b.beforeFileCommit(plan.abs)
		}
	}
	if recorder := checkpoint.RecorderFromContext(ctx); recorder != nil {
		for _, plan := range plans {
			if err := recorder.TrackFileBeforeMutation(plan.abs); err != nil {
				return err
			}
		}
	}
	for _, plan := range plans {
		if err := verifyFilePlanCurrent(plan); err != nil {
			return err
		}
	}
	for _, plan := range plans {
		if plan.remove {
			if err := os.Remove(plan.abs); err != nil {
				return fmt.Errorf("remove %s: %w", plan.path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(plan.abs), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", plan.path, err)
		}
		if err := os.WriteFile(plan.abs, plan.afterBytes, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", plan.path, err)
		}
	}
	return nil
}

func verifyFilePlanCurrent(plan fileCommitPlan) error {
	current, err := os.ReadFile(plan.abs)
	if err != nil {
		if os.IsNotExist(err) {
			if plan.expectedExists {
				return fileConflictError{path: plan.path, reason: "file was deleted before write"}
			}
			return nil
		}
		return fmt.Errorf("read current %s: %w", plan.path, err)
	}
	if !plan.expectedExists {
		return fileConflictError{path: plan.path, reason: "file was created before write"}
	}
	if !bytes.Equal(current, plan.expectedBytes) {
		return fileConflictError{path: plan.path, reason: "file changed before write"}
	}
	return nil
}

type fileConflictError struct {
	path   string
	reason string
}

func (e fileConflictError) Error() string {
	if e.path == "" {
		return e.reason
	}
	return fmt.Sprintf("%s: %s", e.path, e.reason)
}

func isFileConflict(err error) bool {
	var conflict fileConflictError
	return errors.As(err, &conflict)
}
