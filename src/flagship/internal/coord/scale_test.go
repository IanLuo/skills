package coord_test

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// TestScaleAtTenThousandTasks measures the provisional performance target from the
// spec's KPI section: claim and status at 10,000 task records should sit under 1
// second p95 on this host. It is opt-in because it seeds 10,000 rows and a suite
// that takes a minute per run gets skipped instead of trusted.
//
//	FS_SCALE_TEST=1 go test ./internal/coord -run Scale -v
//
// The number this prints is the evidence for the target; the design's revisit
// trigger (a status query above 0.3 s p95) is read off the same run.
func TestScaleAtTenThousandTasks(t *testing.T) {
	if os.Getenv("FS_SCALE_TEST") == "" {
		t.Skip("set FS_SCALE_TEST=1 to measure the 10,000-task sizing case")
	}
	s, _ := open(t)
	const tasks = 10000
	seedStart := time.Now()
	extra := `{"goal":"scale","checks":["true"],"goal_hash":"x"}`
	for i := 0; i < tasks; i++ {
		if err := s.PutTask(memory.Scope("scale"), fmt.Sprintf("t-%08d", i), "scale", extra); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	t.Logf("seeded %d tasks in %s", tasks, time.Since(seedStart).Round(time.Millisecond))

	// Status: one listing plus one in-process pass.
	durations := make([]time.Duration, 0, 5)
	for i := 0; i < 5; i++ {
		start := time.Now()
		rep, err := coord.Status(s, coord.StatusInput{Project: "scale"})
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		durations = append(durations, time.Since(start))
		if got := len(rep.Projects[0].Tasks); got != tasks {
			t.Fatalf("status listed %d tasks, want %d", got, tasks)
		}
	}
	p95 := durations[len(durations)-1]
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("status over %d tasks: min %s median %s max %s",
		tasks, durations[0].Round(time.Millisecond),
		durations[len(durations)/2].Round(time.Millisecond), p95.Round(time.Millisecond))

	// Claim: one listing, one bind, one claim. Selection scans the project, so
	// this is the worst case for the pull path.
	start := time.Now()
	if _, err := coord.Claim(s, coord.ClaimInput{Project: "scale", Session: "s-scale"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	t.Logf("claim of the oldest eligible task among %d: %s", tasks, time.Since(start).Round(time.Millisecond))

	if p95 > time.Second {
		t.Fatalf("status took %s at %d tasks; the target is under 1s", p95, tasks)
	}
}
