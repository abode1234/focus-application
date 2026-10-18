package api_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abode1234/focus-cli/internal/api"
	"github.com/abode1234/focus-cli/internal/client"
	"github.com/abode1234/focus-cli/internal/clock"
	"github.com/abode1234/focus-cli/internal/engine"
	"github.com/abode1234/focus-cli/internal/store"
	"github.com/abode1234/focus-cli/internal/wire"
)

// serve runs the API on a socket in a temp dir and returns a client for it.
func serve(t *testing.T, clk clock.Clock, opts engine.Options) *client.Client {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(repo, clk, opts)
	srv := &api.Server{Engine: eng, Heartbeat: 50 * time.Millisecond}

	dir, err := os.MkdirTemp("", "focus-api")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)
	t.Cleanup(func() {
		hs.Close()
		repo.Close()
		os.RemoveAll(dir)
	})
	return client.New(sock)
}

func TestFullFlowOverSocket(t *testing.T) {
	c := serve(t, clock.Real{}, engine.Options{AutoBreak: true, BreakDuration: time.Minute})
	ctx := context.Background()

	if err := c.Health(ctx); err != nil {
		t.Fatal(err)
	}
	sess, err := c.Start(ctx, "write the design", 25*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sess.EndsAt-sess.StartedAt != 1500 {
		t.Fatalf("ends_at: %+v", sess)
	}
	if _, err := c.Start(ctx, "again", time.Minute); !client.IsStatus(err, http.StatusConflict) {
		t.Fatalf("second start: %v", err)
	}

	st, err := c.Status(ctx)
	if err != nil || st.Session == nil || st.Session.ID != sess.ID || st.RemainingSeconds <= 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}

	res, err := c.Finish(ctx, sess.ID, "wrote it", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.Status != "completed" || res.Break == nil {
		t.Fatalf("finish: %+v", res)
	}
	if _, err := c.Finish(ctx, sess.ID, "", nil); !client.IsStatus(err, http.StatusConflict) {
		t.Fatalf("double finish: %v", err)
	}
	if _, err := c.Abandon(ctx, res.Break.ID); err != nil {
		t.Fatal(err)
	}

	list, err := c.History(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(list) != 2 || list[0].Reflection != "wrote it" {
		t.Fatalf("history: %+v, %v", list, err)
	}
	stats, err := c.Stats(ctx, "day")
	if err != nil || stats.Completed != 1 || stats.FocusSeconds > 1 {
		t.Fatalf("stats: %+v, %v", stats, err)
	}
}

func TestErrorCodes(t *testing.T) {
	c := serve(t, clock.Real{}, engine.Options{})
	ctx := context.Background()
	if _, err := c.Start(ctx, "", time.Minute); !client.IsStatus(err, http.StatusUnprocessableEntity) {
		t.Errorf("empty goal: %v", err)
	}
	if _, err := c.Start(ctx, "x", 0); !client.IsStatus(err, http.StatusUnprocessableEntity) {
		t.Errorf("zero duration: %v", err)
	}
	if _, err := c.Finish(ctx, 42, "", nil); !client.IsStatus(err, http.StatusNotFound) {
		t.Errorf("missing session: %v", err)
	}
	if _, err := c.Stats(ctx, "year"); !client.IsStatus(err, http.StatusUnprocessableEntity) {
		t.Errorf("bad range: %v", err)
	}
}

func TestConcurrentFinishOverAPI(t *testing.T) {
	c := serve(t, clock.Real{}, engine.Options{})
	ctx := context.Background()
	sess, _ := c.Start(ctx, "x", time.Minute)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := c.Finish(ctx, sess.ID, "", nil)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case client.IsStatus(err, http.StatusConflict):
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
}

func TestEventStream(t *testing.T) {
	fake := clock.NewFake(time.Now())
	c := serve(t, fake, engine.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan wire.Event, 16)
	go c.Watch(ctx, func(ev wire.Event) { events <- ev })
	next := func() wire.Event {
		t.Helper()
		select {
		case ev := <-events:
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for event")
			return wire.Event{}
		}
	}
	if ev := next(); ev.Type != "connected" {
		t.Fatalf("first event %q", ev.Type)
	}

	sess, err := c.Start(ctx, "stream", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Type != engine.EventStarted || ev.Session.ID != sess.ID {
		t.Fatalf("got %+v", ev)
	}
	fake.Advance(time.Minute)
	if ev := next(); ev.Type != engine.EventTimeUp {
		t.Fatalf("got %+v", ev)
	}
	c.Finish(ctx, sess.ID, "", nil)
	if ev := next(); ev.Type != engine.EventFinished {
		t.Fatalf("got %+v", ev)
	}
}

func TestDaemonDown(t *testing.T) {
	c := client.New(filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := c.Status(context.Background()); err != client.ErrDaemonDown {
		t.Fatalf("got %v, want ErrDaemonDown", err)
	}
}

func TestOverflowingDurationRejected(t *testing.T) {
	c := serve(t, clock.Real{}, engine.Options{})
	sock := c.Socket()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	for _, body := range []string{
		`{"goal":"x","planned_seconds":18446745574}`,
		`{"goal":"x","planned_seconds":-5}`,
	} {
		resp, err := hc.Post("http://x/v1/sessions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", body, resp.StatusCode)
		}
	}
}

func TestEmptyChunkedFinishBody(t *testing.T) {
	c := serve(t, clock.Real{}, engine.Options{})
	sess, _ := c.Start(context.Background(), "x", time.Minute)
	sock := c.Socket()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	req, _ := http.NewRequest(http.MethodPost, "http://x/v1/sessions/"+strconv.FormatInt(sess.ID, 10)+"/finish", io.NopCloser(strings.NewReader("")))
	req.ContentLength = -1
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty chunked body: %d", resp.StatusCode)
	}
}
