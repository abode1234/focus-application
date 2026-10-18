//go:build gui

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func onHyprland() bool { return os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" }

type hyprWindow struct {
	Address string `json:"address"`
	At      [2]int `json:"at"`
	Size    [2]int `json:"size"`
	Pid     int    `json:"pid"`
}

type hyprMonitor struct {
	X       int  `json:"x"`
	Y       int  `json:"y"`
	Width   int  `json:"width"`
	Height  int  `json:"height"`
	Focused bool `json:"focused"`
}

func hyprJSON(v any, args ...string) error {
	out, err := exec.Command("hyprctl", append(args, "-j")...).Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

func hyprFind(pid int) (hyprWindow, bool) {
	var clients []hyprWindow
	if hyprJSON(&clients, "clients") != nil {
		return hyprWindow{}, false
	}
	for _, c := range clients {
		if c.Pid == pid {
			return c, true
		}
	}
	return hyprWindow{}, false
}

// hyprPlace floats, sizes and moves our window as soon as it maps, hands
// focus back to whatever had it, then keeps saving the geometry so the next
// clock opens where the user left this one.
func hyprPlace(ctx context.Context, pid int, g Geometry, statePath string, pin bool) {
	var previous hyprWindow
	_ = hyprJSON(&previous, "activewindow")

	var win hyprWindow
	for found := false; !found; win, found = hyprFind(pid) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if g.X < 0 || g.Y < 0 {
		g.X, g.Y = defaultCorner(g)
	}
	target := "pid:" + fmt.Sprint(pid)
	cmds := []string{
		"dispatch setfloating " + target,
		fmt.Sprintf("dispatch resizewindowpixel exact %d %d,%s", g.Width, g.Height, target),
		fmt.Sprintf("dispatch movewindowpixel exact %d %d,%s", g.X, g.Y, target),
	}
	// Only the digits should show: no compositor border, shadow, rounding
	// or blur behind the transparent window.
	for _, prop := range []string{"border_size 0", "no_shadow 1", "rounding 0", "no_blur 1"} {
		cmds = append(cmds, fmt.Sprintf("dispatch setprop address:%s %s", win.Address, prop))
	}
	if pin {
		cmds = append(cmds, "dispatch pin "+target)
	}
	if previous.Address != "" && previous.Address != win.Address {
		cmds = append(cmds, "dispatch focuswindow address:"+previous.Address)
	}
	_ = exec.Command("hyprctl", "--batch", strings.Join(cmds, " ; ")).Run()

	if statePath == "" {
		return
	}
	last := g
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		w, ok := hyprFind(pid)
		if !ok {
			continue
		}
		now := Geometry{X: w.At[0], Y: w.At[1], Width: w.Size[0], Height: w.Size[1]}
		if now != last && now.Width > 0 && now.Height > 0 {
			if SaveGeometry(statePath, now) == nil {
				last = now
			}
		}
	}
}

// defaultCorner puts a first-time clock near the top-right of the focused
// monitor.
func defaultCorner(g Geometry) (int, int) {
	var monitors []hyprMonitor
	if hyprJSON(&monitors, "monitors") == nil {
		for _, m := range monitors {
			if m.Focused {
				return m.X + m.Width - g.Width - 32, m.Y + 56
			}
		}
	}
	return 32, 56
}
