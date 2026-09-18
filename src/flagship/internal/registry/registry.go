// Package registry manages the global project registry at ~/.fs/registry.db.
// It is separate from the per-project event store — a lightweight index so
// an agent can discover all managed projects without the operator listing them.
package registry

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Project is a row in the registry.
type Project struct {
	Name         string    `json:"name"`
	RootPath     string    `json:"root_path"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
}

// Registry wraps the global SQLite registry database.
type Registry struct {
	db *sql.DB
}

// busyTimeoutMS is how long a command waits for the registry's write lock before
// giving up with SQLITE_BUSY.
const busyTimeoutMS = "5000"

// Open creates or opens a registry at path. Sets WAL mode and creates schema.
func Open(path string) (*Registry, error) {
	// _busy_timeout is a DSN parameter because it is per-connection: the driver
	// applies it to every connection the pool opens.
	db, err := sql.Open("sqlite", path+"?_busy_timeout="+busyTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("registry: open %s: %w", path, err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry: set WAL: %w", err)
	}

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Registry{db: db}, nil
}

// Close releases the database connection.
func (r *Registry) Close() error {
	return r.db.Close()
}

// JournalMode returns the current journal mode (for testing).
func (r *Registry) JournalMode() (string, error) {
	var mode string
	err := r.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

// BusyTimeout returns how long a connection waits for the write lock, in ms
// (for testing).
func (r *Registry) BusyTimeout() (int, error) {
	var ms int
	err := r.db.QueryRow("PRAGMA busy_timeout").Scan(&ms)
	return ms, err
}

func createSchema(db *sql.DB) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS projects (
	name          TEXT PRIMARY KEY,
	root_path     TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	last_activity TEXT NOT NULL
);`
	_, err := db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("registry: create schema: %w", err)
	}
	return nil
}

// Register adds or updates a project in the registry.
// On conflict (same name), updates root_path and last_activity but preserves created_at.
func (r *Registry) Register(name, rootPath string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO projects (name, root_path, created_at, last_activity)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET root_path=excluded.root_path, last_activity=excluded.last_activity`,
		name, rootPath, now, now,
	)
	if err != nil {
		return fmt.Errorf("registry: register %s: %w", name, err)
	}
	return nil
}

// Get returns a single project by name.
func (r *Registry) Get(name string) (Project, error) {
	var p Project
	var createdAt, lastActivity string
	err := r.db.QueryRow(
		"SELECT name, root_path, created_at, last_activity FROM projects WHERE name = ?",
		name,
	).Scan(&p.Name, &p.RootPath, &createdAt, &lastActivity)
	if err == sql.ErrNoRows {
		return Project{}, fmt.Errorf("registry: project %q not found", name)
	}
	if err != nil {
		return Project{}, fmt.Errorf("registry: get %s: %w", name, err)
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	p.LastActivity, _ = time.Parse(time.RFC3339Nano, lastActivity)
	return p, nil
}

// List returns all registered projects, ordered by last_activity descending.
func (r *Registry) List() ([]Project, error) {
	rows, err := r.db.Query(
		"SELECT name, root_path, created_at, last_activity FROM projects ORDER BY last_activity DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("registry: list: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt, lastActivity string
		if err := rows.Scan(&p.Name, &p.RootPath, &createdAt, &lastActivity); err != nil {
			return nil, fmt.Errorf("registry: scan: %w", err)
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		p.LastActivity, _ = time.Parse(time.RFC3339Nano, lastActivity)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// UpdateActivity sets last_activity to now for the named project.
// No-op if the project doesn't exist.
func (r *Registry) UpdateActivity(name string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		"UPDATE projects SET last_activity = ? WHERE name = ?",
		now, name,
	)
	if err != nil {
		return fmt.Errorf("registry: update activity %s: %w", name, err)
	}
	return nil
}

// Resolve returns the name of the registered project owning path — the deepest
// root_path that is path itself or an ancestor of it. ok is false when no
// registered project covers path.
func (r *Registry) Resolve(path string) (string, bool, error) {
	projects, err := r.List()
	if err != nil {
		return "", false, err
	}

	path = filepath.Clean(path)
	name, depth := "", -1
	for _, p := range projects {
		root := filepath.Clean(p.RootPath)
		if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > depth {
			name, depth = p.Name, len(root)
		}
	}
	return name, depth >= 0, nil
}
