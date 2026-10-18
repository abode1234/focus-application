package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "focus")
	if err := privateDir(dir); err != nil {
		t.Fatalf("fresh dir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(dir); err == nil {
		t.Fatal("accepted a dir other users can enter")
	}
}
