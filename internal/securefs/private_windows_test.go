//go:build windows

package securefs

import (
	"path/filepath"
	"testing"
)

func TestWindowsPrivatePathProtectedACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	status := CheckPrivatePath(dir)
	if !status.Protected {
		t.Fatalf("directory ACL should be protected: %+v", status)
	}

	path := filepath.Join(dir, "credentials.json")
	if err := WritePrivateFile(path, []byte("{}")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	status = CheckPrivatePath(path)
	if !status.Protected {
		t.Fatalf("file ACL should be protected: %+v", status)
	}
}
