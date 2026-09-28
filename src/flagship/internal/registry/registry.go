// Package registry manages the global project registry in ~/.fs/flagship.db.
// It is separate from the per-project event store — a lightweight index so
// an agent can discover all managed projects without the operator listing them.
package registry

import (
	"database/sql"
	"fmt"
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

// Agent is a row in the agents table.
type Agent struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	PaneID    string `json:"pane_id"`
	Workspace string `json:"workspace,omitempty"`
	Project   string `json:"project,omitempty"`
	Status    string `json:"status"`
	Playbook  string `json:"playbook,omitempty"`
	LastSeen  string `json:"last_seen"`
	CreatedAt string `json:"created_at"`
}

// Registry wraps the global SQLite registry database.
type Registry struct {
	db *sql.DB
}

// Open creates or opens a registry at path. Sets WAL mode and creates schema.
func Open(path string) (*Registry, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("registry: open %s: %w", path, err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry: set WAL: %w", err)
	}

	// PRAGMA busy_timeout: needed because last_seen touch makes every fs
	// call a write, increasing SQLITE_BUSY risk under concurrent agents.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry: set busy_timeout: %w", err)
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

// DB returns the underlying database for testing.
func (r *Registry) DB() *sql.DB {
	return r.db
}

// JournalMode returns the current journal mode (for testing).
func (r *Registry) JournalMode() (string, error) {
	var mode string
	err := r.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

func createSchema(db *sql.DB) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS projects (
	name          TEXT PRIMARY KEY,
	root_path     TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	last_activity TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
	name       TEXT PRIMARY KEY,
	role       TEXT NOT NULL,
	pane_id    TEXT NOT NULL,
	workspace  TEXT NOT NULL DEFAULT '',
	project    TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'active',
	playbook   TEXT NOT NULL DEFAULT '',
	last_seen  TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_role ON agents(role);
CREATE INDEX IF NOT EXISTS idx_agents_project ON agents(project);`
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

// --- Agent Registry Methods ---

// validRoles are the allowed agent roles.
var validRoles = map[string]bool{"captain": true, "worker": true, "reviewer": true}

// validAgentStatuses are the allowed agent statuses.
var validAgentStatuses = map[string]bool{"active": true, "idle": true, "dead": true}

// RegisterAgent inserts or replaces an agent registration.
// Enforces single-captain per project: errors if an active captain exists
// for the same project (unless it's the same name being re-registered).
func (r *Registry) RegisterAgent(a Agent) error {
	if a.Name == "" {
		return fmt.Errorf("registry: agent name is required")
	}
	if !validRoles[a.Role] {
		return fmt.Errorf("registry: invalid role %q (valid: captain, worker, reviewer)", a.Role)
	}
	if a.PaneID == "" {
		return fmt.Errorf("registry: pane_id is required")
	}

	// Single-captain enforcement (DESIGN-roles §10 Q2).
	if a.Role == "captain" && a.Project != "" {
		var existingName string
		err := r.db.QueryRow(
			`SELECT name FROM agents WHERE role='captain' AND project=? AND status!='dead' AND name!=?`,
			a.Project, a.Name,
		).Scan(&existingName)
		if err == nil {
			return fmt.Errorf("registry: active captain %q already registered for project %q; deregister it first or wait for stale-detection", existingName, a.Project)
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("registry: captain check: %w", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if a.Status == "" {
		a.Status = "active"
	}

	_, err := r.db.Exec(
		`INSERT INTO agents (name, role, pane_id, workspace, project, status, playbook, last_seen, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   role=excluded.role, pane_id=excluded.pane_id, workspace=excluded.workspace,
		   project=excluded.project, status=excluded.status, playbook=excluded.playbook,
		   last_seen=excluded.last_seen`,
		a.Name, a.Role, a.PaneID, a.Workspace, a.Project, a.Status, a.Playbook, now, now,
	)
	if err != nil {
		return fmt.Errorf("registry: register agent %s: %w", a.Name, err)
	}
	return nil
}

// GetAgent returns a single agent by name.
func (r *Registry) GetAgent(name string) (Agent, error) {
	var a Agent
	err := r.db.QueryRow(
		`SELECT name, role, pane_id, workspace, project, status, playbook, last_seen, created_at
		 FROM agents WHERE name = ?`, name,
	).Scan(&a.Name, &a.Role, &a.PaneID, &a.Workspace, &a.Project, &a.Status, &a.Playbook, &a.LastSeen, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return Agent{}, fmt.Errorf("registry: agent %q not found", name)
	}
	if err != nil {
		return Agent{}, fmt.Errorf("registry: get agent %s: %w", name, err)
	}
	return a, nil
}

// GetAgentByPane returns the agent registered in the given pane.
func (r *Registry) GetAgentByPane(paneID string) (Agent, error) {
	var a Agent
	err := r.db.QueryRow(
		`SELECT name, role, pane_id, workspace, project, status, playbook, last_seen, created_at
		 FROM agents WHERE pane_id = ?`, paneID,
	).Scan(&a.Name, &a.Role, &a.PaneID, &a.Workspace, &a.Project, &a.Status, &a.Playbook, &a.LastSeen, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return Agent{}, fmt.Errorf("registry: no agent in pane %q", paneID)
	}
	if err != nil {
		return Agent{}, fmt.Errorf("registry: get agent by pane %s: %w", paneID, err)
	}
	return a, nil
}

// AgentFilter specifies optional filters for ListAgents.
type AgentFilter struct {
	Role    string
	Project string
	Status  string
}

// ListAgents returns agents matching the given filters.
func (r *Registry) ListAgents(f AgentFilter) ([]Agent, error) {
	query := `SELECT name, role, pane_id, workspace, project, status, playbook, last_seen, created_at FROM agents WHERE 1=1`
	var args []any
	if f.Role != "" {
		query += " AND role = ?"
		args = append(args, f.Role)
	}
	if f.Project != "" {
		query += " AND project = ?"
		args = append(args, f.Project)
	}
	if f.Status != "" {
		query += " AND status = ?"
		args = append(args, f.Status)
	}
	query += " ORDER BY created_at ASC"

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.Name, &a.Role, &a.PaneID, &a.Workspace, &a.Project, &a.Status, &a.Playbook, &a.LastSeen, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("registry: scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// TouchAgent updates last_seen for the agent in the given pane.
// No-op if no agent is registered in that pane.
func (r *Registry) TouchAgent(paneID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = r.db.Exec("UPDATE agents SET last_seen = ? WHERE pane_id = ?", now, paneID)
}

// HeartbeatAgent updates last_seen for an agent by name.
func (r *Registry) HeartbeatAgent(name string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec("UPDATE agents SET last_seen = ? WHERE name = ?", now, name)
	if err != nil {
		return fmt.Errorf("registry: heartbeat %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("registry: agent %q not found", name)
	}
	return nil
}

// UpdateAgent updates mutable fields of an agent.
func (r *Registry) UpdateAgent(name string, status, project string) error {
	if name == "" {
		return fmt.Errorf("registry: agent name is required")
	}

	sets := []string{}
	args := []any{}

	if status != "" {
		if !validAgentStatuses[status] {
			return fmt.Errorf("registry: invalid status %q (valid: active, idle, dead)", status)
		}
		sets = append(sets, "status = ?")
		args = append(args, status)
	}
	if project != "" {
		sets = append(sets, "project = ?")
		args = append(args, project)
	}

	if len(sets) == 0 {
		return fmt.Errorf("registry: nothing to update")
	}

	// Always touch last_seen on update.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sets = append(sets, "last_seen = ?")
	args = append(args, now)
	args = append(args, name)

	q := "UPDATE agents SET " + joinStrings(sets, ", ") + " WHERE name = ?"
	res, err := r.db.Exec(q, args...)
	if err != nil {
		return fmt.Errorf("registry: update agent %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("registry: agent %q not found", name)
	}
	return nil
}

// DeregisterAgent removes an agent from the registry.
func (r *Registry) DeregisterAgent(name string) error {
	if name == "" {
		return fmt.Errorf("registry: agent name is required")
	}
	res, err := r.db.Exec("DELETE FROM agents WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("registry: deregister agent %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("registry: agent %q not found", name)
	}
	return nil
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
