package composer

import (
	"fmt"
	"strings"
)

func (c *Composer) HandlePaste(value string) {
	c.ensureInitialized()
	if c.hasSelection() {
		c.deleteSelection()
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if len([]rune(value)) > largePasteCharThreshold {
		c.textarea.InsertString(c.addPendingPaste(value))
	} else {
		c.textarea.InsertString(value)
	}
	c.markRawCacheStale()
	c.selectionRuneOffset = -1
	c.prunePendingPastes()
	c.reflow()
}

func (c *Composer) collapseLargeValue(value string) string {
	if len([]rune(value)) <= largePasteCharThreshold {
		return value
	}
	return c.addPendingPaste(value)
}

func (c *Composer) addPendingPaste(value string) string {
	placeholder := c.nextLargePastePlaceholder(len([]rune(value)))
	c.pendingPastes = append(c.pendingPastes, pendingPaste{
		placeholder: placeholder,
		text:        value,
	})
	return placeholder
}

func (c *Composer) nextLargePastePlaceholder(charCount int) string {
	if c.largePasteCounts == nil {
		c.largePasteCounts = map[int]int{}
	}
	c.largePasteCounts[charCount]++
	base := fmt.Sprintf("[Pasted Content %d chars]", charCount)
	if c.largePasteCounts[charCount] == 1 {
		return base
	}
	return fmt.Sprintf("%s #%d", base, c.largePasteCounts[charCount])
}

func (c Composer) expandPendingPastes(value string) string {
	for _, paste := range c.pendingPastes {
		value = strings.ReplaceAll(value, paste.placeholder, paste.text)
	}
	return value
}

func (c *Composer) prunePendingPastes() {
	if len(c.pendingPastes) == 0 {
		return
	}
	value := c.rawValue()
	kept := c.pendingPastes[:0]
	for _, paste := range c.pendingPastes {
		if strings.Contains(value, paste.placeholder) {
			kept = append(kept, paste)
		}
	}
	c.pendingPastes = kept
}
