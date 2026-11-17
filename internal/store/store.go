// Package store persists sessions, events, plans and metrics in SQLite.
// The database is shared by the CLI and the dashboard server (WAL mode).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zerosetup/zerosetup/internal/sessions"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  source TEXT NOT NULL,
  repository TEXT NOT NULL,
  commit_sha TEXT NOT NULL DEFAULT '',
  framework TEXT NOT NULL DEFAULT '',
  runtime TEXT NOT NULL DEFAULT '',
  plan_hash TEXT NOT NULL DEFAULT '',
  prev_plan_hash TEXT NOT NULL DEFAULT '',
  approved_at TEXT,
  url TEXT NOT NULL DEFAULT '',
  host_port INTEGER NOT NULL DEFAULT 0,
  container_id TEXT NOT NULL DEFAULT '',
  image_digest TEXT NOT NULL DEFAULT '',
  milestones TEXT NOT NULL DEFAULT '{}',
  error TEXT,
  facts TEXT,
  runner_pid INTEGER NOT NULL DEFAULT 0,
  attempt INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated_at);
CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  time TEXT NOT NULL,
  type TEXT NOT NULL,
  stage TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL,
  data TEXT
);
CREATE INDEX IF NOT EXISTS events_session ON events(session_id, seq);
CREATE TABLE IF NOT EXISTS plans (
  hash TEXT PRIMARY KEY,
  json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS metrics (
  session_id TEXT NOT NULL,
  attempt INTEGER NOT NULL,
  name TEXT NOT NULL,
  millis INTEGER NOT NULL,
  recorded_at TEXT NOT NULL,
  PRIMARY KEY (session_id, attempt, name)
);
`

// Store is the SQLite-backed persistence layer.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot create data directory")
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot open database")
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot initialise database schema")
	}
	_ = os.Chmod(path, 0o600)
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Save inserts or updates a session.
func (s *Store) Save(ctx context.Context, x *sessions.Session) error {
	ms, _ := json.Marshal(x.Milestones)
	var errJSON, approved, facts any
	if x.Error != nil {
		b, _ := json.Marshal(struct {
			*zerr.Error
			CauseText string `json:"cause,omitempty"`
		}{x.Error, x.Error.CauseText()})
		errJSON = string(b)
	}
	if x.ApprovedAt != nil {
		approved = ts(*x.ApprovedAt)
	}
	if len(x.Facts) > 0 {
		facts = string(x.Facts)
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	x.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (id,state,source,repository,commit_sha,framework,runtime,plan_hash,prev_plan_hash,approved_at,url,host_port,container_id,image_digest,milestones,error,facts,runner_pid,attempt,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state, source=excluded.source, repository=excluded.repository,
 commit_sha=excluded.commit_sha, framework=excluded.framework, runtime=excluded.runtime, plan_hash=excluded.plan_hash,
 prev_plan_hash=excluded.prev_plan_hash, approved_at=excluded.approved_at, url=excluded.url, host_port=excluded.host_port,
 container_id=excluded.container_id, image_digest=excluded.image_digest, milestones=excluded.milestones, error=excluded.error,
 facts=excluded.facts, runner_pid=excluded.runner_pid, attempt=excluded.attempt, updated_at=excluded.updated_at`,
		x.ID, string(x.State), x.Source, x.Repository, x.Commit, x.Framework, x.Runtime, x.PlanHash, x.PrevPlanHash, approved,
		x.URL, x.HostPort, x.ContainerID, x.ImageDigest, string(ms), errJSON, facts, x.RunnerPID, x.Attempt, ts(x.CreatedAt), ts(x.UpdatedAt))
	if err != nil {
		return zerr.Wrap(err, zerr.CodeStore, "store", "cannot save session")
	}
	return nil
}

const sessionCols = `id,state,source,repository,commit_sha,framework,runtime,plan_hash,prev_plan_hash,approved_at,url,host_port,container_id,image_digest,milestones,error,facts,runner_pid,attempt,created_at,updated_at`

func scanSession(sc interface{ Scan(...any) error }) (*sessions.Session, error) {
	var x sessions.Session
	var state, ms, created, updated string
	var approved, errJSON, facts sql.NullString
	if err := sc.Scan(&x.ID, &state, &x.Source, &x.Repository, &x.Commit, &x.Framework, &x.Runtime, &x.PlanHash, &x.PrevPlanHash,
		&approved, &x.URL, &x.HostPort, &x.ContainerID, &x.ImageDigest, &ms, &errJSON, &facts, &x.RunnerPID, &x.Attempt, &created, &updated); err != nil {
		return nil, err
	}
	x.State = sessions.State(state)
	x.Milestones = map[sessions.Milestone]time.Time{}
	_ = json.Unmarshal([]byte(ms), &x.Milestones)
	if approved.Valid {
		t := parseTS(approved.String)
		x.ApprovedAt = &t
	}
	if errJSON.Valid {
		var e struct {
			zerr.Error
			CauseText string `json:"cause"`
		}
		if json.Unmarshal([]byte(errJSON.String), &e) == nil {
			ze := e.Error
			if e.CauseText != "" {
				ze.Cause = errors.New(e.CauseText)
			}
			x.Error = &ze
		}
	}
	if facts.Valid {
		x.Facts = json.RawMessage(facts.String)
	}
	x.CreatedAt, x.UpdatedAt = parseTS(created), parseTS(updated)
	return &x, nil
}

// Get loads a session by ID.
func (s *Store) Get(ctx context.Context, id string) (*sessions.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id=?`, id)
	x, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, zerr.New(zerr.CodeSessionNotFound, "session", fmt.Sprintf("session %q not found", id)).
			WithSuggestion("Run `zerosetup ps --all` to list sessions.")
	}
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot load session")
	}
	return x, nil
}

// List returns sessions, newest first. If includeRemoved is false, REMOVED
// sessions are omitted.
func (s *Store) List(ctx context.Context, includeRemoved bool, limit int) ([]*sessions.Session, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + sessionCols + ` FROM sessions`
	if !includeRemoved {
		q += ` WHERE state != 'REMOVED'`
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot list sessions")
	}
	defer rows.Close()
	var out []*sessions.Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot read session")
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AppendEvent persists an event and sets its Seq.
func (s *Store) AppendEvent(ctx context.Context, e *sessions.Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	var data any
	if len(e.Data) > 0 {
		b, _ := json.Marshal(e.Data)
		data = string(b)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO events(session_id,time,type,stage,message,data) VALUES (?,?,?,?,?,?)`,
		e.SessionID, ts(e.Time), e.Type, e.Stage, e.Message, data)
	if err != nil {
		return zerr.Wrap(err, zerr.CodeStore, "store", "cannot record event")
	}
	e.Seq, _ = res.LastInsertId()
	return nil
}

// Events returns events for a session with seq > after.
func (s *Store) Events(ctx context.Context, sessionID string, after int64, limit int) ([]sessions.Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,session_id,time,type,stage,message,data FROM events WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, sessionID, after, limit)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot read events")
	}
	defer rows.Close()
	var out []sessions.Event
	for rows.Next() {
		var e sessions.Event
		var t string
		var data sql.NullString
		if err := rows.Scan(&e.Seq, &e.SessionID, &t, &e.Type, &e.Stage, &e.Message, &data); err != nil {
			return nil, err
		}
		e.Time = parseTS(t)
		if data.Valid {
			_ = json.Unmarshal([]byte(data.String), &e.Data)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SavePlan stores a plan by hash (idempotent).
func (s *Store) SavePlan(ctx context.Context, hash string, planJSON []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO plans(hash,json,created_at) VALUES (?,?,?)`, hash, string(planJSON), ts(time.Now()))
	if err != nil {
		return zerr.Wrap(err, zerr.CodeStore, "store", "cannot save plan")
	}
	return nil
}

// GetPlan loads a plan by full hash.
func (s *Store) GetPlan(ctx context.Context, hash string) ([]byte, error) {
	var j string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM plans WHERE hash=?`, hash).Scan(&j)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, zerr.New(zerr.CodeSessionNotFound, "plan", "plan "+hash+" not found")
	}
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeStore, "store", "cannot load plan")
	}
	return []byte(j), nil
}

// RecordMetric stores a duration metric for a session attempt.
func (s *Store) RecordMetric(ctx context.Context, sessionID string, attempt int, name string, d time.Duration) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO metrics(session_id,attempt,name,millis,recorded_at) VALUES (?,?,?,?,?)`,
		sessionID, attempt, name, d.Milliseconds(), ts(time.Now()))
	return err
}

// Metric is a recorded duration.
type Metric struct {
	Attempt int    `json:"attempt"`
	Name    string `json:"name"`
	Millis  int64  `json:"millis"`
}

// Metrics returns all metrics for a session.
func (s *Store) Metrics(ctx context.Context, sessionID string) ([]Metric, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT attempt,name,millis FROM metrics WHERE session_id=? ORDER BY attempt, recorded_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Metric
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.Attempt, &m.Name, &m.Millis); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
