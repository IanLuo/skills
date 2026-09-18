package store_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/store"
)

// TestQueryLatency100Nodes asserts fs query < 500ms on 100-node store (SPEC R6 NFR).
func TestQueryLatency100Nodes(t *testing.T) {
	s, err := store.Open(tempDB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	projectID := "bench-project"

	// Seed project.
	s.Append(store.Event{
		Type:      store.ProjectCreated,
		ProjectID: projectID,
		Payload:   json.RawMessage(`{"name":"bench","root_path":"/bench"}`),
	})

	// Seed 100 nodes with multiple events each (task-created + status-changed + decision).
	// Total: 100 task-created + 100 status-changed + 50 decisions = 250+ events.
	for i := 0; i < 100; i++ {
		nodeID := fmt.Sprintf("node-%04d", i)
		var goal string
		if i%10 == 0 {
			goal = fmt.Sprintf("implement authentication feature %d with OAuth2 and JWT tokens", i)
		} else if i%5 == 0 {
			goal = fmt.Sprintf("optimize database query performance for table %d", i)
		} else {
			goal = fmt.Sprintf("task %d for module %d refactoring", i, i%7)
		}

		payload, _ := json.Marshal(map[string]string{"goal": goal})
		s.Append(store.Event{
			Type:      store.TaskCreated,
			ProjectID: projectID,
			NodeID:    &nodeID,
			Payload:   payload,
		})

		statusPayload, _ := json.Marshal(map[string]string{"from": "pending", "to": "active"})
		s.Append(store.Event{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			Payload:   statusPayload,
		})

		if i%2 == 0 {
			var summary string
			if i%10 == 0 {
				summary = fmt.Sprintf("decided to use JWT authentication for service %d", i)
			} else {
				summary = fmt.Sprintf("chose caching strategy for component %d", i)
			}
			decPayload, _ := json.Marshal(map[string]string{"summary": summary})
			s.Append(store.Event{
				Type:      store.DecisionRecorded,
				ProjectID: projectID,
				NodeID:    &nodeID,
				Payload:   decPayload,
			})
		}
	}

	// Verify we have 50+ events.
	allEvents, _ := s.Replay(projectID, nil)
	if len(allEvents) < 50 {
		t.Fatalf("expected 50+ events, got %d", len(allEvents))
	}
	t.Logf("seeded %d events across 100 nodes", len(allEvents))

	// Benchmark: query must complete in < 500ms.
	queries := []string{"auth", "JWT", "database", "refactoring", "caching"}
	for _, q := range queries {
		start := time.Now()
		results, err := s.Search(projectID, q)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}

		if elapsed > 500*time.Millisecond {
			t.Errorf("Search(%q) took %v (> 500ms limit)", q, elapsed)
		} else {
			t.Logf("Search(%q): %d results in %v", q, len(results), elapsed)
		}
	}

	// Also benchmark Replay (fs status / fs log).
	start := time.Now()
	_, err = s.Replay(projectID, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Replay took %v (> 500ms limit)", elapsed)
	} else {
		t.Logf("Replay (all events): %v", elapsed)
	}
}

// BenchmarkSearch100Nodes is a Go benchmark for continuous tracking.
func BenchmarkSearch100Nodes(b *testing.B) {
	dir := b.TempDir()
	s, err := store.Open(dir + "/bench.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	projectID := "bench"
	s.Append(store.Event{
		Type:      store.ProjectCreated,
		ProjectID: projectID,
		Payload:   json.RawMessage(`{"name":"bench","root_path":"/bench"}`),
	})

	for i := 0; i < 100; i++ {
		nodeID := fmt.Sprintf("node-%04d", i)
		goal := fmt.Sprintf("task %d authentication JWT database migration", i)
		payload, _ := json.Marshal(map[string]string{"goal": goal})
		s.Append(store.Event{
			Type:      store.TaskCreated,
			ProjectID: projectID,
			NodeID:    &nodeID,
			Payload:   payload,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Search(projectID, "authentication")
	}
}
