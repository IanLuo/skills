// Package store implements the append-only event store backed by SQLite.
// It is the sole owner of the SQLite schema and ULID generation.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// EventType enumerates the hardcoded event types (SPEC R3, SYSTEM-DESIGN R2).
type EventType string

// busyTimeoutMS is how long a command waits for the shared store's write lock
// before giving up with SQLITE_BUSY.
const busyTimeoutMS = "5000"

const (
	ProjectCreated   EventType = "project-created"
	TaskCreated      EventType = "task-created"
	StatusChanged    EventType = "status-changed"
	DecisionRecorded EventType = "decision-recorded"
	KnowledgeAdded   EventType = "knowledge-added"
	TaskBlocked      EventType = "task-blocked"
	TaskUnblocked    EventType = "task-unblocked"
	MetadataChanged  EventType = "metadata-changed"
)

var validTypes = map[EventType]bool{
	ProjectCreated:   true,
	TaskCreated:      true,
	StatusChanged:    true,
	DecisionRecorded: true,
	KnowledgeAdded:   true,
	TaskBlocked:      true,
	TaskUnblocked:    true,
	MetadataChanged:  true,
}

// Event is the core persistent entity (SYSTEM-DESIGN R2).
type Event struct {
	ID           string          `json:"id"`
	Timestamp    time.Time       `json:"timestamp"`
	Type         EventType       `json:"type"`
	ProjectID    string          `json:"project_id"`
	NodeID       *string         `json:"node_id"`
	ParentNodeID *string         `json:"parent_node_id"`
	CommitSHA    *string         `json:"commit_sha"`
	Payload      json.RawMessage `json:"payload"`
}

// ReplayFilter narrows the events returned by Replay.
type ReplayFilter struct {
	Types  []EventType
	NodeID *string
}

// Store wraps a SQLite database for the append-only event log.
type Store struct {
	db   *sql.DB
	ulid *ULIDGenerator
	path string
}

// Open creates or opens a store at path. Sets WAL mode and creates schema.
func Open(path string) (*Store, error) {
	// One store serves every project, so parallel commands contend for a single
	// write lock. _busy_timeout is a DSN parameter because it is per-connection:
	// the driver applies it to every connection the pool opens.
	db, err := sql.Open("sqlite", path+"?_busy_timeout="+busyTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// WAL mode for crash safety (SPEC R6, SYSTEM-DESIGN R2).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: set WAL: %w", err)
	}
	// Foreign keys off — we don't use them; events are self-contained.
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: pragma: %w", err)
	}

	// Header and schema damage is refused here: journal_mode and createSchema both
	// read the file and fail with SQLITE_NOTADB / SQLITE_CORRUPT. A full
	// PRAGMA integrity_check is deliberately not run — it is linear in file size
	// (≈4ms/MB, ~2s on a 144MB store) and this one store now serves every project,
	// so it would tax every command. SQLite reports corruption when it reads a
	// damaged page, which is the same refusal, later (SPEC R9, SYSTEM-DESIGN R4).
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, ulid: NewULIDGenerator(), path: path}, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Path returns the file path of the database.
func (s *Store) Path() string {
	return s.path
}

// JournalMode returns the current journal mode (for testing WAL).
func (s *Store) JournalMode() (string, error) {
	var mode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

// BusyTimeout returns how long a connection waits for the write lock, in ms
// (for testing).
func (s *Store) BusyTimeout() (int, error) {
	var ms int
	err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&ms)
	return ms, err
}

func createSchema(db *sql.DB) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS events (
	id             TEXT PRIMARY KEY,
	timestamp      TEXT NOT NULL,
	type           TEXT NOT NULL,
	project_id     TEXT NOT NULL,
	node_id        TEXT,
	parent_node_id TEXT,
	commit_sha     TEXT,
	payload        TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_events_project ON events(project_id);
CREATE INDEX IF NOT EXISTS idx_events_project_node ON events(project_id, node_id);
CREATE INDEX IF NOT EXISTS idx_events_project_type ON events(project_id, type);

-- FTS5 virtual table for full-text search on payloads (SYSTEM-DESIGN R2).
-- Standalone (not external content) — small overhead, simpler queries.
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
	event_id UNINDEXED,
	project_id UNINDEXED,
	payload
);
`
	_, err := db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("store: create schema: %w", err)
	}
	return nil
}

// Append validates and persists an event. Returns the generated ULID.
// The event's ID and Timestamp are set by the store (ARCHITECTURE R4).
func (s *Store) Append(evt Event) (string, error) {
	if err := validate(evt); err != nil {
		return "", err
	}

	id := s.ulid.New()
	ts := time.Now().UTC()

	payload := string(evt.Payload)
	if payload == "" {
		payload = "{}"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO events (id, timestamp, type, project_id, node_id, parent_node_id, commit_sha, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		ts.Format(time.RFC3339Nano),
		string(evt.Type),
		evt.ProjectID,
		nilStr(evt.NodeID),
		nilStr(evt.ParentNodeID),
		nilStr(evt.CommitSHA),
		payload,
	)
	if err != nil {
		return "", fmt.Errorf("store: append event: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO events_fts (event_id, project_id, payload) VALUES (?, ?, ?)`,
		id, evt.ProjectID, payload,
	)
	if err != nil {
		return "", fmt.Errorf("store: append fts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: commit: %w", err)
	}
	return id, nil
}

// AppendBatch validates and persists multiple events in a single transaction.
// All events succeed or all fail (atomic). Returns the generated ULIDs.
func (s *Store) AppendBatch(events []Event) ([]string, error) {
	// Validate all events before starting the transaction.
	for i, evt := range events {
		if err := validate(evt); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback()

	ids := make([]string, len(events))
	ts := time.Now().UTC()

	for i, evt := range events {
		id := s.ulid.New()
		ids[i] = id

		payload := string(evt.Payload)
		if payload == "" {
			payload = "{}"
		}

		_, err = tx.Exec(
			`INSERT INTO events (id, timestamp, type, project_id, node_id, parent_node_id, commit_sha, payload)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id,
			ts.Format(time.RFC3339Nano),
			string(evt.Type),
			evt.ProjectID,
			nilStr(evt.NodeID),
			nilStr(evt.ParentNodeID),
			nilStr(evt.CommitSHA),
			payload,
		)
		if err != nil {
			return nil, fmt.Errorf("store: append event %d: %w", i, err)
		}

		_, err = tx.Exec(
			`INSERT INTO events_fts (event_id, project_id, payload) VALUES (?, ?, ?)`,
			id, evt.ProjectID, payload,
		)
		if err != nil {
			return nil, fmt.Errorf("store: append fts %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return ids, nil
}

// Replay returns events for a project in insertion order, optionally filtered.
func (s *Store) Replay(projectID string, filter *ReplayFilter) ([]Event, error) {
	query := `SELECT id, timestamp, type, project_id, node_id, parent_node_id, commit_sha, payload
	          FROM events WHERE project_id = ?`
	args := []any{projectID}

	if filter != nil {
		if len(filter.Types) > 0 {
			placeholders := make([]string, len(filter.Types))
			for i, t := range filter.Types {
				placeholders[i] = "?"
				args = append(args, string(t))
			}
			query += " AND type IN (" + strings.Join(placeholders, ",") + ")"
		}
		if filter.NodeID != nil {
			query += " AND node_id = ?"
			args = append(args, *filter.NodeID)
		}
	}

	query += " ORDER BY id ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: replay: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// Search performs FTS5 prefix search on event payloads within a project.
// Each term is treated as a prefix to improve recall (e.g. "auth" matches "authentication").
// If the FTS5 table is corrupt or missing, it auto-rebuilds from events and retries
// (SYSTEM-DESIGN R2: FTS5 index is disposable, self-heals).
func (s *Store) Search(projectID, query string) ([]Event, error) {
	results, err := s.searchFTS(projectID, query)
	if err == nil {
		return results, nil
	}

	// FTS5 query failed — attempt self-healing rebuild.
	if rebuildErr := s.rebuildFTS(); rebuildErr != nil {
		return nil, fmt.Errorf("store: search failed and rebuild failed: search=%w, rebuild=%v", err, rebuildErr)
	}

	// Retry after rebuild.
	return s.searchFTS(projectID, query)
}

// searchFTS runs the actual FTS5 query.
func (s *Store) searchFTS(projectID, query string) ([]Event, error) {
	query = prefixQuery(query)
	const q = `
		SELECT e.id, e.timestamp, e.type, e.project_id, e.node_id, e.parent_node_id, e.commit_sha, e.payload
		FROM events_fts f
		JOIN events e ON e.id = f.event_id
		WHERE f.project_id = ? AND events_fts MATCH ?
		ORDER BY f.rank`

	rows, err := s.db.Query(q, projectID, query)
	if err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// rebuildFTS drops and recreates the FTS5 index from the events table.
func (s *Store) rebuildFTS() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: rebuild fts begin: %w", err)
	}
	defer tx.Rollback()

	// Drop existing (may or may not exist).
	_, _ = tx.Exec("DROP TABLE IF EXISTS events_fts")

	// Recreate.
	_, err = tx.Exec(`CREATE VIRTUAL TABLE events_fts USING fts5(
		event_id UNINDEXED,
		project_id UNINDEXED,
		payload
	)`)
	if err != nil {
		return fmt.Errorf("store: rebuild fts create: %w", err)
	}

	// Reinsert from events table.
	_, err = tx.Exec(`INSERT INTO events_fts (event_id, project_id, payload)
		SELECT id, project_id, payload FROM events`)
	if err != nil {
		return fmt.Errorf("store: rebuild fts insert: %w", err)
	}

	return tx.Commit()
}

// ExecDirect executes raw SQL on the database. For testing only.
func (s *Store) ExecDirect(sql string) error {
	_, err := s.db.Exec(sql)
	return err
}

// LastEventID returns the most recent event ID for a project, or "" if none.
func (s *Store) LastEventID(projectID string) (string, error) {
	var id sql.NullString
	err := s.db.QueryRow(
		"SELECT id FROM events WHERE project_id = ? ORDER BY id DESC LIMIT 1",
		projectID,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: last_event_id: %w", err)
	}
	if id.Valid {
		return id.String, nil
	}
	return "", nil
}

// validate checks structural rules (SYSTEM-DESIGN R3).
func validate(evt Event) error {
	if !validTypes[evt.Type] {
		return fmt.Errorf("store: unknown event type: %s", evt.Type)
	}
	if evt.ProjectID == "" {
		return fmt.Errorf("store: project_id is required")
	}
	// project-created is the only type that accepts nil node_id (SYSTEM-DESIGN R2).
	if evt.Type != ProjectCreated && evt.NodeID == nil {
		return fmt.Errorf("store: node_id is required for event type %s", evt.Type)
	}
	return nil
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var (
			e   Event
			ts  string
			nid sql.NullString
			pid sql.NullString
			sha sql.NullString
			pay string
		)
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.ProjectID, &nid, &pid, &sha, &pay); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil, fmt.Errorf("store: parse timestamp %q: %w", ts, err)
		}
		e.Timestamp = t
		if nid.Valid {
			e.NodeID = &nid.String
		}
		if pid.Valid {
			e.ParentNodeID = &pid.String
		}
		if sha.Valid {
			e.CommitSHA = &sha.String
		}
		e.Payload = json.RawMessage(pay)
		events = append(events, e)
	}
	return events, rows.Err()
}

// prefixQuery converts each whitespace-separated term to a prefix match.
// e.g. "auth decisions" → "auth* decisions*"
func prefixQuery(q string) string {
	parts := strings.Fields(q)
	for i, p := range parts {
		if !strings.HasSuffix(p, "*") {
			parts[i] = p + "*"
		}
	}
	return strings.Join(parts, " ")
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
