package gui

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abode1234/focus-cli/internal/api"
	"github.com/abode1234/focus-cli/internal/client"
	"github.com/abode1234/focus-cli/internal/clock"
	"github.com/abode1234/focus-cli/internal/engine"
	"github.com/abode1234/focus-cli/internal/store"
	"github.com/abode1234/focus-cli/internal/wire"
)

func daemon(t *testing.T) *client.Client {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(repo, clock.Real{}, engine.Options{AutoBreak: true, BreakDuration: 5 * time.Minute})
	dir, _ := os.MkdirTemp("", "focus-gui")
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	cfg := wire.Config{DefaultSeconds: 1500, PresetSeconds: []int64{1500, 3000}, BreakSeconds: 300, AutoBreak: true}
	hs := &http.Server{Handler: (&api.Server{Engine: eng, Config: cfg}).Handler()}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close(); repo.Close(); os.RemoveAll(dir) })
	return client.New(sock)
}

type browser struct {
	t    *testing.T
	base string
	http *http.Client
}

func open(t *testing.T, c *client.Client, popup bool) *browser {
	srv := httptest.NewServer(newServer(c, "secret", popup).Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, base: srv.URL, http: &http.Client{Jar: jar}}
	if code, _ := b.get("/?t=secret"); code != 200 {
		t.Fatalf("page with token: %d", code)
	}
	return b
}

func (b *browser) get(path string) (int, string) {
	resp, err := b.http.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (b *browser) post(path string, form url.Values) string {
	resp, err := b.http.PostForm(b.base+path, form)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		b.t.Fatalf("POST %s: %d %s", path, resp.StatusCode, body)
	}
	return string(body)
}

func TestRequiresToken(t *testing.T) {
	srv := httptest.NewServer(newServer(daemon(t), "secret", false).Handler())
	defer srv.Close()
	for _, path := range []string{"/", "/?t=wrong", "/panel", "/state"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s without token: %d", path, resp.StatusCode)
		}
	}
	resp, _ := http.PostForm(srv.URL+"/start", url.Values{"goal": {"x"}, "preset": {"60"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /start without token: %d", resp.StatusCode)
	}
}

func TestStartFinishThroughWindow(t *testing.T) {
	c := daemon(t)
	b := open(t, c, true)
	ctx := context.Background()

	if _, state := b.get("/state"); state != "idle" {
		t.Fatalf("state %q", state)
	}
	html := b.post("/start", url.Values{"goal": {"Write the design"}, "preset": {"custom"}, "custom": {"40m"}})
	if !strings.Contains(html, "Write the design") || !strings.Contains(html, "data-ends-at") {
		t.Fatalf("running panel:\n%s", html)
	}
	st, _ := c.Status(ctx)
	if st.Session == nil || st.Session.PlannedSeconds != 2400 {
		t.Fatalf("daemon status: %+v", st.Session)
	}

	html = b.post("/finish", url.Values{"id": {strconv.FormatInt(st.Session.ID, 10)}, "reflection": {"outlined it"}})
	if !strings.Contains(html, "data-close-after") {
		t.Fatalf("popup should close after saving:\n%s", html)
	}
	list, _ := c.History(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if len(list) != 1 || list[0].Reflection != "outlined it" {
		t.Fatalf("history: %+v", list)
	}
	if _, state := b.get("/state"); state != "idle" {
		t.Fatalf("unchecked break box should skip the break, state %q", state)
	}
}

func TestErrorsRenderInPanel(t *testing.T) {
	b := open(t, daemon(t), false)
	html := b.post("/start", url.Values{"goal": {""}, "preset": {"1500"}})
	if !strings.Contains(html, `class="error"`) {
		t.Fatalf("empty goal should show an error:\n%s", html)
	}
}

func TestEventsSurviveDaemonRestart(t *testing.T) {
	dir, _ := os.MkdirTemp("", "focus-gui-restart")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	b := open(t, client.New(sock), false) // daemon not running yet

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/events", nil)
	resp, err := b.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/events with daemon down: %d (EventSource gives up on non-200)", resp.StatusCode)
	}

	repo, _ := store.Open(":memory:")
	defer repo.Close()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: (&api.Server{Engine: engine.New(repo, clock.Real{}, engine.Options{})}).Handler()}
	go hs.Serve(ln)
	defer hs.Close()

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var all strings.Builder
		for {
			n, err := resp.Body.Read(buf)
			all.Write(buf[:n])
			if strings.Contains(all.String(), "data: connected") {
				got <- all.String()
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no change event after the daemon came up")
	}
}

func clockPage(t *testing.T, c *client.Client) (*browser, func() string) {
	t.Helper()
	srv := httptest.NewServer(newServer(c, "secret", false).Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, base: srv.URL, http: &http.Client{Jar: jar}}
	if code, _ := b.get("/clock?t=secret"); code != 200 {
		t.Fatalf("clock with token: %d", code)
	}
	return b, func() string {
		_, html := b.get("/clock")
		return html
	}
}

// clockText is what the clock window shows: the body's visible text.
func clockText(html string) string {
	start := strings.Index(html, `<span id="t">`) + len(`<span id="t">`)
	return html[start : strings.Index(html[start:], "</span>")+start]
}

func TestClockShowsOnlyDigits(t *testing.T) {
	c := daemon(t)
	_, page := clockPage(t, c)
	if got := clockText(page()); got != "" {
		t.Fatalf("idle clock shows %q, want nothing", got)
	}
	if _, err := c.Start(context.Background(), "Write", 25*time.Minute); err != nil {
		t.Fatal(err)
	}
	html := page()
	got := clockText(html)
	if got != "25:00" && got != "24:59" {
		t.Fatalf("running clock shows %q, want the countdown", got)
	}
	for _, chrome := range []string{"<button", "<form", "<input", "class=\"card\"", "Write"} {
		if strings.Contains(html, chrome) {
			t.Errorf("clock page contains %q; it should show only the digits", chrome)
		}
	}
}

func TestClockEmptyDuringBreak(t *testing.T) {
	c := daemon(t)
	_, page := clockPage(t, c)
	if _, err := c.StartBreak(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := clockText(page()); got != "" {
		t.Fatalf("break clock shows %q; the clock follows focus sessions only", got)
	}
}

func TestClockRequiresToken(t *testing.T) {
	srv := httptest.NewServer(newServer(daemon(t), "secret", false).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/clock?t=wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("clock with a wrong token: %d", resp.StatusCode)
	}
}
