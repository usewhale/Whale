//go:build !windows

package tools

func decodeTextBytes(b []byte) string {
	return string(b)
}

func decodeTextFileBytes(b []byte) (string, textEncoding) {
	return string(b), textEncodingRaw
}

func encodeTextFileString(s string, _ textEncoding) []byte {
	return []byte(s)
}
