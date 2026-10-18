// Package api exposes the engine over HTTP+JSON and SSE. It maps requests to
// engine calls and renders no UI.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/abode1234/focus-cli/internal/engine"
	"github.com/abode1234/focus-cli/internal/model"
	"github.com/abode1234/focus-cli/internal/wire"
)

// Server serves the daemon API.
type Server struct {
	Engine *engine.Engine
	Config wire.Config
	Logger *slog.Logger
	// Heartbeat is the SSE keep-alive interval (default 15s).
	Heartbeat time.Duration
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/config", s.config)
	mux.HandleFunc("POST /v1/sessions", s.start)
	mux.HandleFunc("POST /v1/breaks", s.startBreak)
	mux.HandleFunc("POST /v1/sessions/{id}/finish", s.finish)
	mux.HandleFunc("POST /v1/sessions/{id}/abandon", s.abandon)
	mux.HandleFunc("GET /v1/sessions", s.history)
	mux.HandleFunc("GET /v1/stats", s.stats)
	mux.HandleFunc("GET /v1/events", s.events)
	return mux
}

func (s *Server) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Config)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st, err := s.Engine.Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Status{
		Now:              st.Now.Unix(),
		Session:          wire.Ptr(st.Session),
		RemainingSeconds: int64(st.Remaining / time.Second),
		TimeUp:           st.TimeUp,
	})
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var req wire.StartRequest
	if !decode(w, r, &req) {
		return
	}
	d, err := duration(req.PlannedSeconds)
	if err != nil {
		s.fail(w, err)
		return
	}
	sess, err := s.Engine.Start(r.Context(), req.Goal, d)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.FromModel(sess))
}

func (s *Server) startBreak(w http.ResponseWriter, r *http.Request) {
	var req wire.BreakRequest
	if !decode(w, r, &req) {
		return
	}
	var d time.Duration
	if req.PlannedSeconds != 0 {
		var err error
		if d, err = duration(req.PlannedSeconds); err != nil {
			s.fail(w, err)
			return
		}
	}
	sess, err := s.Engine.StartBreak(r.Context(), d)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.FromModel(sess))
}

func (s *Server) finish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req wire.FinishRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.Engine.Finish(r.Context(), id, req.Reflection, req.StartBreak)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.FinishResponse{Session: wire.FromModel(res.Session), Break: wire.Ptr(res.Break)})
}

func (s *Server) abandon(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sess, err := s.Engine.Abandon(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.FromModel(sess))
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	from, err := unixParam(r, "from", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	to, err := unixParam(r, "to", time.Now().Add(time.Hour).Unix())
	if err != nil {
		s.fail(w, err)
		return
	}
	list, err := s.Engine.History(r.Context(), time.Unix(from, 0), time.Unix(to, 0))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := wire.Sessions{Sessions: make([]wire.Session, 0, len(list))}
	for _, sess := range list {
		out.Sessions = append(out.Sessions, wire.FromModel(sess))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	rng := engine.Range(r.URL.Query().Get("range"))
	if rng == "" {
		rng = engine.RangeDay
	}
	st, err := s.Engine.Stats(r.Context(), rng)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := wire.Stats{
		Range: string(st.Range), From: st.From.Unix(), To: st.To.Unix(),
		Completed: st.Completed, Abandoned: st.Abandoned, FocusSeconds: st.FocusSeconds,
		Days: make([]wire.Day, 0, len(st.Days)),
	}
	for _, d := range st.Days {
		out.Days = append(out.Days, wire.Day(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel := s.Engine.Subscribe()
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	interval := s.Heartbeat
	if interval <= 0 {
		interval = 15 * time.Second
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()

	for {
		var ev wire.Event
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			ev = wire.Event{Type: "heartbeat"}
		case e, ok := <-events:
			if !ok {
				return
			}
			sess := wire.FromModel(e.Session)
			ev = wire.Event{Type: e.Type, Session: &sess}
		}
		data, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
			return
		}
		flusher.Flush()
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, model.ErrConflict), errors.Is(err, model.ErrNotRunning):
		code = http.StatusConflict
	case errors.Is(err, model.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, model.ErrInvalid):
		code = http.StatusUnprocessableEntity
	default:
		s.log().Error("request failed", "err", err)
	}
	writeJSON(w, code, wire.Error{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// duration converts planned_seconds, rejecting values that would overflow
// time.Duration before the engine can see them.
func duration(seconds int64) (time.Duration, error) {
	if seconds < 1 || seconds > int64(engine.MaxDuration/time.Second) {
		return 0, fmt.Errorf("%w: planned_seconds must be between 1 and %d", model.ErrInvalid, int64(engine.MaxDuration/time.Second))
	}
	return time.Duration(seconds) * time.Second, nil
}

// decode reads an optional JSON body; an empty body leaves v at its zero value.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, wire.Error{Error: "invalid JSON: " + err.Error()})
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, wire.Error{Error: model.ErrNotFound.Error()})
		return 0, false
	}
	return id, true
}

func unixParam(r *http.Request, name string, fallback int64) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be Unix seconds", model.ErrInvalid, name)
	}
	return n, nil
}
