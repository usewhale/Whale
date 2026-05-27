package securefs

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	privateDirMode  = 0o700
	privateFileMode = 0o600
)

type PrivatePathStatus struct {
	Protected bool
	Detail    string
}

func MkdirPrivate(path string) error {
	if err := os.MkdirAll(path, privateDirMode); err != nil {
		return err
	}
	return hardenPrivatePath(path, true)
}

func WritePrivateFile(path string, data []byte) error {
	if err := MkdirPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, privateFileMode); err != nil {
		return err
	}
	return hardenPrivatePath(path, false)
}

func OpenPrivateAppend(path string) (*os.File, error) {
	if err := MkdirPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, privateFileMode)
	if err != nil {
		return nil, err
	}
	if err := hardenPrivatePath(path, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func OpenPrivateTruncate(path string) (*os.File, error) {
	if err := MkdirPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, privateFileMode)
	if err != nil {
		return nil, err
	}
	if err := hardenPrivatePath(path, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func CheckPrivatePath(path string) PrivatePathStatus {
	status, err := checkPrivatePath(path)
	if err != nil {
		return PrivatePathStatus{
			Protected: false,
			Detail:    fmt.Sprintf("%s ACL check failed: %v", path, err),
		}
	}
	return status
}
