//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func withWindowsANSIPage(t *testing.T, page uint32) {
	t.Helper()
	prev := windowsANSIPage
	windowsANSIPage = func() uint32 { return page }
	t.Cleanup(func() { windowsANSIPage = prev })
}

func TestWindowsDecodeTextBytesDecodesGB18030WhenUTF8Invalid(t *testing.T) {
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("拒绝访问 - \\"))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}
	if got := decodeTextBytes(gbk); got != "拒绝访问 - \\" {
		t.Fatalf("decodeTextBytes = %q", got)
	}
}

func TestWindowsReadFileDecodesGB18030Text(t *testing.T) {
	withWindowsANSIPage(t, 936)
	dir := t.TempDir()
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文内容\n第二行\n"))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gbk.txt"), gbk, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "gbk.txt",
	}))
	if err != nil || res.IsError {
		t.Fatalf("read_file failed: err=%v res=%+v", err, res)
	}
	content := readFileContent(t, readFileData(t, res))
	if content != "中文内容\n第二行" {
		t.Fatalf("content = %q", content)
	}
}

func TestWindowsEditFilePreservesGB18030Bytes(t *testing.T) {
	withWindowsANSIPage(t, 936)
	dir := t.TempDir()
	before, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文 OLD\r\n第二行\r\n"))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}
	path := filepath.Join(dir, "gbk.txt")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	readFileFull(t, ts, "gbk.txt")
	res, err := ts.editFile(context.Background(), tc("edit_file", map[string]any{
		"file_path": "gbk.txt",
		"search":    "OLD",
		"replace":   "NEW",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit_file failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	want, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文 NEW\r\n第二行\r\n"))
	if err != nil {
		t.Fatalf("encode expected GB18030: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("edited bytes = % x, want % x", got, want)
	}
}

func TestWindowsEditFileMatchesVisibleGB18030Text(t *testing.T) {
	withWindowsANSIPage(t, 936)
	dir := t.TempDir()
	before, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文内容\r\n第二行\r\n"))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}
	path := filepath.Join(dir, "gbk.txt")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	readFileFull(t, ts, "gbk.txt")
	res, err := ts.editFile(context.Background(), tc("edit_file", map[string]any{
		"file_path": "gbk.txt",
		"search":    "中文内容",
		"replace":   "中文结果",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit_file failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	want, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文结果\r\n第二行\r\n"))
	if err != nil {
		t.Fatalf("encode expected GB18030: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("edited bytes = % x, want % x", got, want)
	}
}

func TestWindowsEditFilePreservesNonChineseLegacyBytes(t *testing.T) {
	withWindowsANSIPage(t, 932)
	dir := t.TempDir()
	before, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("日本語 OLD\r\n次の行\r\n"))
	if err != nil {
		t.Fatalf("encode Shift-JIS fixture: %v", err)
	}
	path := filepath.Join(dir, "shiftjis.txt")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	readFileFull(t, ts, "shiftjis.txt")
	res, err := ts.editFile(context.Background(), tc("edit_file", map[string]any{
		"file_path": "shiftjis.txt",
		"search":    "OLD",
		"replace":   "NEW",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit_file failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	want, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("日本語 NEW\r\n次の行\r\n"))
	if err != nil {
		t.Fatalf("encode expected Shift-JIS: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("edited bytes = % x, want % x", got, want)
	}
}

func TestWindowsGoSearchFallbackDecodesGB18030Text(t *testing.T) {
	withWindowsANSIPage(t, 936)
	dir := t.TempDir()
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("alpha\n中文目标\n"))
	if err != nil {
		t.Fatalf("encode GB18030 fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gbk.txt"), gbk, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	matches, byFile, _, err := searchWithGo("中文目标", dir, "*.txt", true, defaultGrepLimit, func(path string) string {
		return filepath.Base(path)
	})
	if err != nil {
		t.Fatalf("searchWithGo: %v", err)
	}
	if len(matches) != 1 || byFile["gbk.txt"] != 1 {
		t.Fatalf("matches=%+v byFile=%+v", matches, byFile)
	}
	if matches[0].LineNumber != 2 || !strings.Contains(matches[0].Line, "中文目标") {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}
