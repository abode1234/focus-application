package gui

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abode1234/focus-cli/internal/client"
	"github.com/abode1234/focus-cli/internal/durfmt"
	"github.com/abode1234/focus-cli/internal/wire"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const cookieName = "focus_token"

type server struct {
	c     *client.Client
	token string
	popup bool
	tmpl  *template.Template
}

func newServer(c *client.Client, token string, popup bool) *server {
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"clock": func(s int64) string { return durfmt.Clock(time.Duration(s) * time.Second) },
		"human": func(s int64) string { return durfmt.Human(time.Duration(s) * time.Second) },
		"hhmm":  func(unix int64) string { return time.Unix(unix, 0).Format("15:04") },
	}).ParseFS(templateFS, "templates/*.html"))
	return &server{c: c, token: token, popup: popup, tmpl: tmpl}
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /clock", s.clock)
	mux.HandleFunc("GET /panel", s.panel)
	mux.HandleFunc("GET /state", s.state)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("POST /start", s.start)
	mux.HandleFunc("POST /finish", s.finish)
	mux.HandleFunc("POST /abandon", s.abandon)
	mux.HandleFunc("POST /break", s.startBreak)
	return s.authorize(mux)
}

// authorize stops other local users and web pages from driving the daemon
// through this loopback port. The token arrives once in the URL and is kept
// as a cookie.
func (s *server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" && (r.URL.Path == "/" || r.URL.Path == "/clock") && s.valid(t) {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && s.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}

func (s *server) valid(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

type preset struct {
	Seconds int64
	Label   string
	Checked bool
}

type panelData struct {
	State     string
	Status    wire.Status
	Session   *wire.Session
	Presets   []preset
	Config    wire.Config
	Error     string
	Notice    string
	CloseSoon bool
	Today     wire.Stats
}

// stateKey names what the panel shows. The page swaps the panel only when
// it changes, so a half-typed reflection survives unrelated events.
func stateKey(st wire.Status) string {
	switch s := st.Session; {
	case s == nil:
		return "idle"
	case s.IsBreak():
		return fmt.Sprintf("break-%d", s.ID)
	case st.TimeUp:
		return fmt.Sprintf("timeup-%d", s.ID)
	default:
		return fmt.Sprintf("focus-%d", s.ID)
	}
}

func (s *server) load(ctx context.Context) (panelData, error) {
	st, err := s.c.Status(ctx)
	if err != nil {
		return panelData{}, err
	}
	cfg, err := s.c.Config(ctx)
	if err != nil {
		return panelData{}, err
	}
	d := panelData{State: stateKey(st), Status: st, Session: st.Session, Config: cfg}
	if st.Session == nil {
		if today, err := s.c.Stats(ctx, "day"); err == nil {
			d.Today = today
		}
	}
	for i, sec := range cfg.PresetSeconds {
		d.Presets = append(d.Presets, preset{Seconds: sec, Label: durfmt.Human(time.Duration(sec) * time.Second), Checked: i == 0})
	}
	return d, nil
}

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, edit func(*panelData)) {
	d, err := s.load(r.Context())
	if err != nil {
		d = panelData{State: "error", Error: friendly(err)}
	}
	if edit != nil {
		edit(&d)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) page(w http.ResponseWriter, r *http.Request)  { s.render(w, r, "page.html", nil) }
func (s *server) clock(w http.ResponseWriter, r *http.Request) { s.render(w, r, "clock.html", nil) }
func (s *server) panel(w http.ResponseWriter, r *http.Request) { s.render(w, r, "panel", nil) }

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	st, err := s.c.Status(r.Context())
	key := "error"
	if err == nil {
		key = stateKey(st)
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, key)
}

// events relays daemon events to the page so it can refresh. It always
// answers 200 and reconnects to the daemon itself: EventSource gives up for
// good on a non-200 reply, which would freeze the window after a daemon
// restart.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()
	s.c.Watch(r.Context(), func(ev wire.Event) {
		fmt.Fprintf(w, "event: change\ndata: %s\n\n", ev.Type)
		flusher.Flush()
	})
}

func (s *server) start(w http.ResponseWriter, r *http.Request) {
	goal := strings.TrimSpace(r.FormValue("goal"))
	d, err := formDuration(r.FormValue("preset"), r.FormValue("custom"))
	if err == nil {
		_, err = s.c.Start(r.Context(), goal, d)
	}
	s.after(w, r, err, "")
}

func formDuration(preset, custom string) (time.Duration, error) {
	if preset == "custom" || (preset == "" && custom != "") {
		return durfmt.Parse(custom)
	}
	sec, err := strconv.ParseInt(preset, 10, 64)
	if err != nil || sec <= 0 {
		return 0, errors.New("pick a duration")
	}
	return time.Duration(sec) * time.Second, nil
}

func (s *server) finish(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		s.after(w, r, errors.New("no session to finish"), "")
		return
	}
	brk := r.FormValue("break") == "on"
	res, err := s.c.Finish(r.Context(), id, r.FormValue("reflection"), &brk)
	notice := ""
	closeSoon := false
	if err == nil && !res.Session.IsBreak() {
		notice = "Saved. Nice work."
		closeSoon = s.popup
	}
	s.render(w, r, "panel", func(d *panelData) {
		if err != nil {
			d.Error = friendly(err)
		}
		d.Notice, d.CloseSoon = notice, closeSoon
	})
}

func (s *server) abandon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err == nil {
		_, err = s.c.Abandon(r.Context(), id)
	}
	s.after(w, r, err, "Session abandoned.")
}

func (s *server) startBreak(w http.ResponseWriter, r *http.Request) {
	_, err := s.c.StartBreak(r.Context(), 0)
	s.after(w, r, err, "")
}

func (s *server) after(w http.ResponseWriter, r *http.Request, err error, notice string) {
	s.render(w, r, "panel", func(d *panelData) {
		if err != nil {
			d.Error = friendly(err)
			return
		}
		d.Notice = notice
	})
}

func friendly(err error) string {
	if errors.Is(err, client.ErrDaemonDown) {
		return "focusd isn't running. Start it with: systemctl --user start focusd"
	}
	return err.Error()
}
