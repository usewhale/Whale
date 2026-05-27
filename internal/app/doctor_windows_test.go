//go:build windows

package app

import (
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/securefs"
)

func TestWindowsDoctorCheckDataDirACLReportsProtected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "whale")
	if err := securefs.MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	got := doctorCheckDataDirACL("windows", dir)
	if got.Level != DoctorOK {
		t.Fatalf("data dir acl check: %+v", got)
	}
}
