//go:build windows

package tools

import (
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func decodeShellOutput(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(decoded)
}
