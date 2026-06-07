package execboundary

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWrapperDeniesPathQualifiedRecursiveRemove(t *testing.T) {
	var stderr bytes.Buffer
	code := RunWrapper([]string{"/bin/rm", "rm", "-rf", "/tmp/target"}, nil, &stderr)
	if code == 0 {
		t.Fatal("recursive remove should be denied")
	}
	if !strings.Contains(stderr.String(), "Whale policy denied shell command: rm -rf") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}
