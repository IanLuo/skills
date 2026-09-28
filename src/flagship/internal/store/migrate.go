package store

import (
	"database/sql"
	"fmt"
)

// MigrateResult summarises a single source migration.
type MigrateResult struct {
	Source   string `json:"source"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
}

// MigrateFrom reads all events from srcPath (old per-project store.db) and
// inserts them into this store, preserving original id, timestamp, and all
// fields. Duplicates (same primary key) are silently skipped.
// The operation is atomic — all events from a source succeed or none do.
func (s *Store) MigrateFrom(srcPath string) (MigrateResult, error) {
	result := MigrateResult{Source: srcPath}

	srcDB, err := sql.Open("sqlite", srcPath+"?mode=ro")
	if err != nil {
		return result, fmt.Errorf("migrate: open source %s: %w", srcPath, err)
	}
	defer srcDB.Close()

	// Read all events from source.
	rows, err := srcDB.Query(
		`SELECT id, timestamp, type, project_id, node_id, parent_node_id, commit_sha, payload
		 FROM events ORDER BY id ASC`)
	if err != nil {
		return result, fmt.Errorf("migrate: read source %s: %w", srcPath, err)
	}
	defer rows.Close()

	type rawEvent struct {
		id, ts, typ, projectID string
		nodeID, parentNodeID   sql.NullString
		commitSHA              sql.NullString
		payload                string
	}

	var events []rawEvent
	for rows.Next() {
		var e rawEvent
		if err := rows.Scan(&e.id, &e.ts, &e.typ, &e.projectID,
			&e.nodeID, &e.parentNodeID, &e.commitSHA, &e.payload); err != nil {
			return result, fmt.Errorf("migrate: scan source %s: %w", srcPath, err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("migrate: iterate source %s: %w", srcPath, err)
	}

	result.Total = len(events)
	if result.Total == 0 {
		return result, nil
	}

	// Insert into destination in a single transaction.
	tx, err := s.db.Begin()
	if err != nil {
		return result, fmt.Errorf("migrate: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtEvt, err := tx.Prepare(
		`INSERT OR IGNORE INTO events
		 (id, timestamp, type, project_id, node_id, parent_node_id, commit_sha, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return result, fmt.Errorf("migrate: prepare event stmt: %w", err)
	}
	defer stmtEvt.Close()

	stmtFTS, err := tx.Prepare(
		`INSERT OR IGNORE INTO events_fts (event_id, project_id, payload)
		 VALUES (?, ?, ?)`)
	if err != nil {
		return result, fmt.Errorf("migrate: prepare fts stmt: %w", err)
	}
	defer stmtFTS.Close()

	for _, e := range events {
		res, err := stmtEvt.Exec(e.id, e.ts, e.typ, e.projectID,
			nullStr(e.nodeID), nullStr(e.parentNodeID), nullStr(e.commitSHA), e.payload)
		if err != nil {
			return result, fmt.Errorf("migrate: insert event %s: %w", e.id, err)
		}
		affected, _ := res.RowsAffected()
		if affected == 1 {
			result.Imported++
			// Only insert FTS row for newly imported events.
			if _, err := stmtFTS.Exec(e.id, e.projectID, e.payload); err != nil {
				return result, fmt.Errorf("migrate: insert fts %s: %w", e.id, err)
			}
		} else {
			result.Skipped++
		}
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("migrate: commit: %w", err)
	}

	// Rebuild FTS index to guarantee consistency — the row-by-row INSERT OR
	// IGNORE into events_fts can silently fail on some FTS5 configurations,
	// and earlier binaries may have migrated events without FTS entries.
	if result.Imported > 0 {
		if err := s.rebuildFTS(); err != nil {
			return result, fmt.Errorf("migrate: rebuild fts: %w", err)
		}
	}

	return result, nil
}

func nullStr(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}
