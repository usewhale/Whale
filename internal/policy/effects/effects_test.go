package effects

import "testing"

func TestExternalDirectoryGrantAllowsDescendantsOnly(t *testing.T) {
	granted := GrantKey(ExternalDirectory, "/outside")

	if !GrantAllowsKey(granted, GrantKey(ExternalDirectory, "/outside/sub/file.go")) {
		t.Fatal("expected external directory grant to cover descendant")
	}
	if GrantAllowsKey(granted, GrantKey(ExternalDirectory, "/outside-other/file.go")) {
		t.Fatal("external directory grant must not cover sibling path")
	}
	if GrantAllowsKey(granted, GrantKey(ReadPath, "/outside/sub/file.go")) {
		t.Fatal("external directory grant must not cover a different effect kind")
	}
}
