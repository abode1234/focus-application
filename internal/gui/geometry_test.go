package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeometryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus", "clock.json")
	if _, ok := LoadGeometry(path); ok {
		t.Fatal("loaded a geometry that was never saved")
	}
	want := Geometry{X: 1100, Y: 40, Width: 300, Height: 120}
	if err := SaveGeometry(path, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadGeometry(path); !ok || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, ok, want)
	}
	os.WriteFile(path, []byte("{garbage"), 0o600)
	if _, ok := LoadGeometry(path); ok {
		t.Fatal("accepted a corrupt file")
	}
}
