package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateFrom(t *testing.T) {
	dir := t.TempDir()

	// Create a source store with some events.
	srcPath := filepath.Join(dir, "source.db")
	src, err := Open(srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}

	nodeID := "task-001"
	payload, _ := json.Marshal(map[string]string{"goal": "test migration"})

	id1, err := src.Append(Event{
		Type:      ProjectCreated,
		ProjectID: "test-proj",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("append project-created: %v", err)
	}

	id2, err := src.Append(Event{
		Type:      TaskCreated,
		ProjectID: "test-proj",
		NodeID:    &nodeID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("append task-created: %v", err)
	}
	src.Close()

	// Create destination store.
	dstPath := filepath.Join(dir, "dest.db")
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	defer dst.Close()

	// Migrate.
	result, err := dst.MigrateFrom(srcPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("total: got %d, want 2", result.Total)
	}
	if result.Imported != 2 {
		t.Errorf("imported: got %d, want 2", result.Imported)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped: got %d, want 0", result.Skipped)
	}

	// Verify events exist in destination.
	events, err := dst.Replay("test-proj", nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events: got %d, want 2", len(events))
	}
	if events[0].ID != id1 {
		t.Errorf("event 0 id: got %s, want %s", events[0].ID, id1)
	}
	if events[1].ID != id2 {
		t.Errorf("event 1 id: got %s, want %s", events[1].ID, id2)
	}

	// Verify FTS works.
	found, err := dst.Search("test-proj", "migration")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("fts results: got %d, want 2", len(found))
	}

	// Migrate again — should skip all.
	result2, err := dst.MigrateFrom(srcPath)
	if err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if result2.Imported != 0 {
		t.Errorf("re-migrate imported: got %d, want 0", result2.Imported)
	}
	if result2.Skipped != 2 {
		t.Errorf("re-migrate skipped: got %d, want 2", result2.Skipped)
	}

	// Verify count unchanged.
	events2, _ := dst.Replay("test-proj", nil)
	if len(events2) != 2 {
		t.Errorf("events after re-migrate: got %d, want 2", len(events2))
	}
}

func TestMigrateFrom_EmptySource(t *testing.T) {
	dir := t.TempDir()

	srcPath := filepath.Join(dir, "empty.db")
	src, _ := Open(srcPath)
	src.Close()

	dstPath := filepath.Join(dir, "dest.db")
	dst, _ := Open(dstPath)
	defer dst.Close()

	result, err := dst.MigrateFrom(srcPath)
	if err != nil {
		t.Fatalf("migrate empty: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("total: got %d, want 0", result.Total)
	}
}

func TestMigrateFrom_BadPath(t *testing.T) {
	dir := t.TempDir()
	dstPath := filepath.Join(dir, "dest.db")
	dst, _ := Open(dstPath)
	defer dst.Close()

	_, err := dst.MigrateFrom(filepath.Join(dir, "nonexistent.db"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestMigrateFrom_FTSConsistency(t *testing.T) {
	// Verifies that after migration, FTS search works even if row-by-row FTS
	// inserts were unreliable (the post-migration rebuild must fix it).
	dir := t.TempDir()

	srcPath := filepath.Join(dir, "source.db")
	src, err := Open(srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	nodeID := "task-001"
	src.Append(Event{
		Type:      ProjectCreated,
		ProjectID: "proj",
		Payload:   json.RawMessage(`{"name":"proj","root_path":"/proj"}`),
	})
	src.Append(Event{
		Type:      TaskCreated,
		ProjectID: "proj",
		NodeID:    &nodeID,
		Payload:   json.RawMessage(`{"goal":"implement authentication"}`),
	})
	src.Close()

	dstPath := filepath.Join(dir, "dest.db")
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	defer dst.Close()

	_, err = dst.MigrateFrom(srcPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// FTS must work immediately after migration.
	results, err := dst.Search("proj", "authentication")
	if err != nil {
		t.Fatalf("search after migrate: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 FTS result, got %d", len(results))
	}
}

func TestMigrateFrom_OverlappingSources(t *testing.T) {
	dir := t.TempDir()

	// Create two sources with overlapping events (same IDs).
	nodeID := "task-001"
	payload, _ := json.Marshal(map[string]string{"goal": "shared task"})

	src1Path := filepath.Join(dir, "src1.db")
	src1, _ := Open(src1Path)
	src1.Append(Event{
		Type: TaskCreated, ProjectID: "proj", NodeID: &nodeID, Payload: payload,
	})
	// Read back the event to get its ID.
	evts1, _ := src1.Replay("proj", nil)
	src1.Close()

	// Create src2 that has the same event (copy the file).
	src2Path := filepath.Join(dir, "src2.db")
	data, _ := os.ReadFile(src1Path)
	os.WriteFile(src2Path, data, 0o644)
	// Also copy WAL if present.
	if wal, err := os.ReadFile(src1Path + "-wal"); err == nil {
		os.WriteFile(src2Path+"-wal", wal, 0o644)
	}

	dstPath := filepath.Join(dir, "dest.db")
	dst, _ := Open(dstPath)
	defer dst.Close()

	r1, _ := dst.MigrateFrom(src1Path)
	r2, _ := dst.MigrateFrom(src2Path)

	if r1.Imported != 1 {
		t.Errorf("src1 imported: got %d, want 1", r1.Imported)
	}
	if r2.Imported != 0 {
		t.Errorf("src2 imported: got %d, want 0 (overlap)", r2.Imported)
	}
	if r2.Skipped != 1 {
		t.Errorf("src2 skipped: got %d, want 1", r2.Skipped)
	}

	// Verify only one event in dest.
	events, _ := dst.Replay("proj", nil)
	if len(events) != 1 {
		t.Errorf("events: got %d, want 1", len(events))
	}
	if events[0].ID != evts1[0].ID {
		t.Errorf("event id mismatch")
	}
}
