//go:build windows

package tools

import (
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func decodeTextBytes(b []byte) string {
	return decodeGB18030Fallback(b)
}

func decodeTextFileBytes(b []byte) (string, textEncoding) {
	if len(b) == 0 {
		return "", textEncodingRaw
	}
	if utf8.Valid(b) {
		return string(b), textEncodingRaw
	}
	if !windowsTextFilesUseGB18030() {
		return string(b), textEncodingRaw
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err != nil {
		return string(b), textEncodingRaw
	}
	return string(decoded), textEncodingGB18030
}

func encodeTextFileString(s string, encoding textEncoding) []byte {
	if encoding != textEncodingGB18030 {
		return []byte(s)
	}
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(s))
	if err != nil {
		return []byte(s)
	}
	return encoded
}

var windowsANSIPage = windows.GetACP

func windowsTextFilesUseGB18030() bool {
	switch windowsANSIPage() {
	case 936, 54936:
		return true
	default:
		return false
	}
}

func decodeGB18030Fallback(b []byte) string {
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
