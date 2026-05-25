//go:build !windows

package tools

func decodeShellOutput(b []byte) string {
	return decodeTextBytes(b)
}
