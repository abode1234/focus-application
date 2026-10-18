// Package client is the library every interface uses to talk to focusd over
// its Unix socket. It holds no state.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abode1234/focus-cli/internal/wire"
)

// ErrDaemonDown means the socket could not be reached.
var ErrDaemonDown = errors.New("focusd is not running (start it with: systemctl --user start focusd, or run: focus daemon)")

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// IsStatus reports whether err is an APIError with the given code.
func IsStatus(err error, code int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == code
}

// Client talks to one daemon socket.
type Client struct {
	socket string
	http   *http.Client // for normal requests, with a timeout
	stream *http.Client // for SSE, without one
}

// New returns a client for the socket at path.
func New(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{
		socket: socket,
		http:   &http.Client{Transport: transport, Timeout: 10 * time.Second},
		stream: &http.Client{Transport: transport},
	}
}

// Socket returns the socket path.
func (c *Client) Socket() string { return c.socket }

// Health checks the daemon is alive.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/health", nil, nil)
}

// Status returns the current session.
func (c *Client) Status(ctx context.Context) (wire.Status, error) {
	var st wire.Status
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &st)
	return st, err
}

// Config returns UI-relevant daemon settings.
func (c *Client) Config(ctx context.Context) (wire.Config, error) {
	var cfg wire.Config
	err := c.do(ctx, http.MethodGet, "/v1/config", nil, &cfg)
	return cfg, err
}

// Start begins a focus session.
func (c *Client) Start(ctx context.Context, goal string, d time.Duration) (wire.Session, error) {
	var s wire.Session
	err := c.do(ctx, http.MethodPost, "/v1/sessions",
		wire.StartRequest{Goal: goal, PlannedSeconds: int64(d / time.Second)}, &s)
	return s, err
}

// StartBreak begins a break; zero uses the daemon's default length.
func (c *Client) StartBreak(ctx context.Context, d time.Duration) (wire.Session, error) {
	var s wire.Session
	err := c.do(ctx, http.MethodPost, "/v1/breaks", wire.BreakRequest{PlannedSeconds: int64(d / time.Second)}, &s)
	return s, err
}

// Finish completes a session. startBreak nil follows the daemon's setting.
func (c *Client) Finish(ctx context.Context, id int64, reflection string, startBreak *bool) (wire.FinishResponse, error) {
	var res wire.FinishResponse
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/sessions/%d/finish", id),
		wire.FinishRequest{Reflection: reflection, StartBreak: startBreak}, &res)
	return res, err
}

// Abandon abandons a session.
func (c *Client) Abandon(ctx context.Context, id int64) (wire.Session, error) {
	var s wire.Session
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/sessions/%d/abandon", id), nil, &s)
	return s, err
}

// History lists sessions started in [from, to).
func (c *Client) History(ctx context.Context, from, to time.Time) ([]wire.Session, error) {
	q := url.Values{}
	q.Set("from", fmt.Sprint(from.Unix()))
	q.Set("to", fmt.Sprint(to.Unix()))
	var out wire.Sessions
	err := c.do(ctx, http.MethodGet, "/v1/sessions?"+q.Encode(), nil, &out)
	return out.Sessions, err
}

// Stats returns stats for "day" or "week".
func (c *Client) Stats(ctx context.Context, rng string) (wire.Stats, error) {
	var st wire.Stats
	err := c.do(ctx, http.MethodGet, "/v1/stats?range="+url.QueryEscape(rng), nil, &st)
	return st, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://focusd"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return wrapDial(err)
	}
	defer resp.Body.Close()
	return readResponse(resp, out)
}

func readResponse(resp *http.Response, out any) error {
	if resp.StatusCode >= 300 {
		var e wire.Error
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func wrapDial(err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return ErrDaemonDown
	}
	return err
}

// Events opens one SSE connection and delivers events until ctx ends or the
// connection drops; then the channel closes. Heartbeats are filtered out.
func (c *Client) Events(ctx context.Context) (<-chan wire.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://focusd/v1/events", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, wrapDial(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, readResponse(resp, nil)
	}
	ch := make(chan wire.Event)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "" && data.Len() > 0:
				var ev wire.Event
				if json.Unmarshal([]byte(data.String()), &ev) == nil && ev.Type != "heartbeat" {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
				data.Reset()
			}
		}
	}()
	return ch, nil
}

// Watch keeps an event stream open, reconnecting with backoff. Every
// (re)connection first emits a synthetic "connected" event so callers can
// re-fetch status; a lost connection emits "disconnected". Watch returns
// when ctx ends.
func (c *Client) Watch(ctx context.Context, fn func(wire.Event)) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		events, err := c.Events(ctx)
		if err == nil {
			backoff = 500 * time.Millisecond
			fn(wire.Event{Type: "connected"})
			for ev := range events {
				fn(ev)
			}
			if ctx.Err() != nil {
				return
			}
			fn(wire.Event{Type: "disconnected"})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
}
