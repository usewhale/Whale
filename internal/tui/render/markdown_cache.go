package render

import (
	"container/list"
	"sync"
)

// markdownCacheCap bounds the number of (input, width, quiet) entries kept in
// memory. During streaming the live assistant message changes on every delta,
// inserting one fresh key per delta — but those live keys are only touched
// once. The prior-turn keys, by contrast, are hit on every re-render, so LRU
// keeps them near the front and the live keys decay off the back. The cap
// therefore only needs to exceed the on-screen prior-turn count by a healthy
// margin, not the streaming delta count. 256 covers typical transcripts; a
// pathological stream that produces more deltas than the cap can still
// transiently evict prior-turn entries, which are simply re-rendered (and
// re-cached) on the next render that touches them.
const markdownCacheCap = 256

// markdownCacheMaxInputBytes caps the size of individual entries to avoid
// retaining pathological payloads (e.g. a multi-MB pasted log).
const markdownCacheMaxInputBytes = 256 * 1024

type markdownCacheKey struct {
	input string
	width int
	quiet bool
}

type markdownCacheEntry struct {
	key    markdownCacheKey
	output string
}

var (
	markdownCacheMu    sync.Mutex
	markdownCacheList  = list.New()
	markdownCacheIndex = make(map[markdownCacheKey]*list.Element, markdownCacheCap)
)

func markdownCacheGet(k markdownCacheKey) (string, bool) {
	markdownCacheMu.Lock()
	defer markdownCacheMu.Unlock()
	elem, ok := markdownCacheIndex[k]
	if !ok {
		return "", false
	}
	markdownCacheList.MoveToFront(elem)
	return elem.Value.(*markdownCacheEntry).output, true
}

func markdownCachePut(k markdownCacheKey, v string) {
	if len(k.input) > markdownCacheMaxInputBytes {
		return
	}
	markdownCacheMu.Lock()
	defer markdownCacheMu.Unlock()
	if elem, ok := markdownCacheIndex[k]; ok {
		markdownCacheList.MoveToFront(elem)
		elem.Value.(*markdownCacheEntry).output = v
		return
	}
	elem := markdownCacheList.PushFront(&markdownCacheEntry{key: k, output: v})
	markdownCacheIndex[k] = elem
	if markdownCacheList.Len() > markdownCacheCap {
		back := markdownCacheList.Back()
		if back != nil {
			markdownCacheList.Remove(back)
			delete(markdownCacheIndex, back.Value.(*markdownCacheEntry).key)
		}
	}
}

// resetMarkdownCacheForTest clears all cache state. Intended for tests only.
func resetMarkdownCacheForTest() {
	markdownCacheMu.Lock()
	defer markdownCacheMu.Unlock()
	markdownCacheList.Init()
	markdownCacheIndex = make(map[markdownCacheKey]*list.Element, markdownCacheCap)
}
