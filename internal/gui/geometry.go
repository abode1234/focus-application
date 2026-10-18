package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Geometry is a window's place on screen. X or Y below zero leaves
// placement to the window manager.
type Geometry struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LoadGeometry reads a remembered geometry. It reports false when there is
// none or the file is unreadable, so callers fall back to their defaults.
func LoadGeometry(path string) (Geometry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Geometry{}, false
	}
	var g Geometry
	if json.Unmarshal(b, &g) != nil || g.Width <= 0 || g.Height <= 0 {
		return Geometry{}, false
	}
	return g, true
}

// SaveGeometry writes g atomically, so a crash never leaves a torn file.
func SaveGeometry(path string, g Geometry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(g)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
