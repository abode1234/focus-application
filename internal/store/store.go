// Package store persists sessions in SQLite. It runs queries and migrations;
// business rules live in the engine.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abode1234/focus-cli/internal/model"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a SQLite-backed session repository.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies
// migrations. Use ":memory:" for a throwaway database.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		q := url.Values{}
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "busy_timeout(5000)")
		q.Add("_pragma", "synchronous(NORMAL)")
		dsn = "file:" + path + "?" + q.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer keeps SQLite simple and makes ":memory:" a single database.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrate applies every embedded migration newer than PRAGMA user_version.
// Files are named NNNN_description.sql.
func (s *Store) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		version, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", base)
		}
		if version <= current {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", base, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const columns = "id, kind, goal, planned_seconds, started_at, ended_at, status, reflection"

// Insert stores a new running session and returns it with its ID.
// It returns model.ErrConflict when another session is already running.
func (s *Store) Insert(ctx context.Context, sess model.Session) (model.Session, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (kind, goal, planned_seconds, started_at, status)
		 VALUES (?, ?, ?, ?, 'running')`,
		sess.Kind, sess.Goal, sess.PlannedSeconds, sess.StartedAt.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return model.Session{}, model.ErrConflict
		}
		return model.Session{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.Session{}, err
	}
	return s.Get(ctx, id)
}

// Get returns one session, or model.ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (model.Session, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM sessions WHERE id = ?", id)
	sess, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Session{}, model.ErrNotFound
	}
	return sess, err
}

// Running returns the running session, if any.
func (s *Store) Running(ctx context.Context) (*model.Session, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM sessions WHERE status = 'running'")
	sess, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// Complete moves a running session to completed. The update is conditioned
// on the session still running, so concurrent calls cannot both succeed.
func (s *Store) Complete(ctx context.Context, id int64, endedAt time.Time, reflection string) (model.Session, error) {
	return s.transition(ctx, id,
		`UPDATE sessions SET status = 'completed', ended_at = ?, reflection = ?
		 WHERE id = ? AND status = 'running'`,
		endedAt.Unix(), nullable(reflection), id)
}

// Abandon moves a running session to abandoned.
func (s *Store) Abandon(ctx context.Context, id int64, endedAt time.Time) (model.Session, error) {
	return s.transition(ctx, id,
		`UPDATE sessions SET status = 'abandoned', ended_at = ?
		 WHERE id = ? AND status = 'running'`,
		endedAt.Unix(), id)
}

func (s *Store) transition(ctx context.Context, id int64, query string, args ...any) (model.Session, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return model.Session{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return model.Session{}, err
	}
	if n == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return model.Session{}, err
		}
		return model.Session{}, model.ErrNotRunning
	}
	return s.Get(ctx, id)
}

// List returns sessions started in [from, to), oldest first.
func (s *Store) List(ctx context.Context, from, to time.Time) ([]model.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+columns+" FROM sessions WHERE started_at >= ? AND started_at < ? ORDER BY started_at, id",
		from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		sess, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (model.Session, error) {
	var (
		sess       model.Session
		started    int64
		ended      sql.NullInt64
		reflection sql.NullString
	)
	err := r.Scan(&sess.ID, &sess.Kind, &sess.Goal, &sess.PlannedSeconds, &started, &ended, &sess.Status, &reflection)
	if err != nil {
		return model.Session{}, err
	}
	sess.StartedAt = time.Unix(started, 0)
	if ended.Valid {
		sess.EndedAt = time.Unix(ended.Int64, 0)
	}
	sess.Reflection = reflection.String
	return sess, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
