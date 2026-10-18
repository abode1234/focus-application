CREATE TABLE sessions (
  id              INTEGER PRIMARY KEY,
  kind            TEXT    NOT NULL DEFAULT 'focus'
                  CHECK (kind IN ('focus', 'break')),
  goal            TEXT    NOT NULL,
  planned_seconds INTEGER NOT NULL CHECK (planned_seconds > 0),
  started_at      INTEGER NOT NULL,          -- Unix time
  ended_at        INTEGER,
  status          TEXT    NOT NULL
                  CHECK (status IN ('running', 'completed', 'abandoned')),
  reflection      TEXT
);

-- Only one running session (focus or break) at any time.
CREATE UNIQUE INDEX one_running
  ON sessions(status) WHERE status = 'running';

-- For daily/weekly stats.
CREATE INDEX idx_sessions_started ON sessions(started_at);
