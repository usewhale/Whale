//go:build !windows

package securefs

import "os"

func hardenPrivatePath(path string, isDir bool) error {
	mode := os.FileMode(privateFileMode)
	if isDir {
		mode = privateDirMode
	}
	return os.Chmod(path, mode)
}

func checkPrivatePath(path string) (PrivatePathStatus, error) {
	return PrivatePathStatus{Protected: true, Detail: path + " uses POSIX private modes"}, nil
}
