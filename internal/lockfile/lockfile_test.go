package lockfile

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: %v", err)
	}
	release()
	release2, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}
