package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type ImmutablePrefix struct {
	systemBlocks []string
	fingerprint  string
}

func NewImmutablePrefix(systemBlocks []string) *ImmutablePrefix {
	p := &ImmutablePrefix{}
	p.Refresh(systemBlocks)
	return p
}

func (p *ImmutablePrefix) Refresh(systemBlocks []string) {
	p.systemBlocks = append([]string(nil), systemBlocks...)
	p.fingerprint = fingerprintSystemBlocks(p.systemBlocks)
}

func (p *ImmutablePrefix) Fingerprint() string {
	if p == nil {
		return ""
	}
	return p.fingerprint
}

func (p *ImmutablePrefix) VerifyFingerprint() (string, bool) {
	if p == nil {
		return "", true
	}
	fresh := fingerprintSystemBlocks(p.systemBlocks)
	return fresh, fresh == p.fingerprint
}

func (p *ImmutablePrefix) SystemBlocks() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.systemBlocks...)
}

func (p *ImmutablePrefix) ToMessages() []core.Message {
	if len(p.systemBlocks) == 0 {
		return nil
	}
	return []core.Message{{
		Role:  core.RoleSystem,
		Text:  strings.Join(p.systemBlocks, "\n\n"),
		Parts: []core.MessagePart{{Type: core.MessagePartText, Text: strings.Join(p.systemBlocks, "\n\n")}},
	}}
}

func fingerprintSystemBlocks(systemBlocks []string) string {
	sum := sha256.Sum256([]byte(strings.Join(systemBlocks, "\n\n")))
	return hex.EncodeToString(sum[:])
}
