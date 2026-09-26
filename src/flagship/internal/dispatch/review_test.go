package dispatch_test

// The independent review: a review-task dispatch's own protocol, and the exit
// gate that terminates a fix loop.
//
// Two things are measured here, and they are the two the change exists for. The
// first is that the reviewer and the self-reviewer are different participants
// and never share a plan: `review` is `applies_when: code` and asks a worker to
// re-read its own diff, `independent-review` is `applies_when: type=review-task`
// and asks a worker to audit somebody else's change. A plan carrying both tells
// one worker to be its own adversary and then to report drift on a stranger's
// work, which is why the tag sets are kept apart by test rather than by hope.
//
// The second is that the review's exit gate is reachable. Its termination
// condition is not "no findings" — a reviewer re-reading a diff always finds
// something, and an exit nobody can pass is a gate that gets bypassed while the
// findings go stale behind it. It is that every finding ENDS somewhere: fixed
// and verified, filed as a named gap, or accepted with a reason. `accepted` is
// the disposition most likely to be unrepresentable, so it is the one the
// passing half of the test exercises.
//
// Both tests read the playbooks the binary ships, out of defaults/kb, rather
// than restating them: a test that wrote its own copies would keep passing
// after the shipped protocols drifted.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/trace"
)

// withShipped gives the fixture's knowledge centre the tree the binary embeds as
// its last scope. It is how a test measures the plan a real dispatch resolves
// rather than one a test assembled — and it writes nothing to disk, which is the
// point: a shipped playbook is live without being materialised first.
func withShipped(t *testing.T, f *fixture) {
	t.Helper()
	kc, err := knowledge.Open(f.fsRoot, os.DirFS(filepath.Join("..", "..", "defaults", "kb")))
	if err != nil {
		t.Fatalf("knowledge.Open: %v", err)
	}
	f.kc = kc
}

// planNames lists a plan's playbook names in plan order.
func planNames(plan []trace.PlanEntry) []string {
	names := make([]string, 0, len(plan))
	for _, entry := range plan {
		names = append(names, entry.Name)
	}
	return names
}

// TestReviewTaskPlanCarriesTheReviewerAndNotTheSelfReview is the regression the
// change names: the two review playbooks must not both load into one plan.
func TestReviewTaskPlanCarriesTheReviewerAndNotTheSelfReview(t *testing.T) {
	f := newFixture(t, t.TempDir())
	withShipped(t, f)

	brief := prepareOnly(t, f, dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "review the landed change",
	})

	names := planNames(brief.Plan)
	if !slices.Contains(names, "independent-review") {
		t.Errorf("plan %v does not carry independent-review", names)
	}
	if slices.Contains(names, "review") {
		t.Errorf("plan %v carries the self-review playbook too: one worker is told to "+
			"re-read its own diff and to audit somebody else's", names)
	}

	// The reviewer's obligations reach the worker through the brief, not
	// through a Go literal: the classification and the no-editing rule are the
	// protocol's, and a brief that dropped them would be a plan naming a
	// playbook whose steps nobody is given.
	joined := strings.Join(brief.Obligations, "\n")
	for _, want := range []string{"against the change", "discovered by the change", "report, do not fix"} {
		if !strings.Contains(joined, want) {
			t.Errorf("brief obligations do not carry %q:\n%s", want, joined)
		}
	}
}

// TestCodePlanStillCarriesTheSelfReview is the other half: nothing about the
// code situation changed. A code dispatch still loads the self-review playbook,
// and never the independent reviewer — a worker is not asked to audit a change
// it is in the middle of writing.
func TestCodePlanStillCarriesTheSelfReview(t *testing.T) {
	f := newFixture(t, t.TempDir())
	withShipped(t, f)

	// HOME is a temp dir so the shipped entry checks — which run real commands,
	// and fs logs every command under ~/.fs — write nothing into the operator's.
	// `code`'s checks cannot pass in a fresh temp project (there is no
	// AGENTS.md), so the dispatch proceeds past them with --confirm, which is
	// what that flag is for.
	t.Setenv("HOME", t.TempDir())

	brief := prepareOnly(t, f, dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: "change the code",
		Tags: []string{"code"}, Confirm: true,
	})

	names := planNames(brief.Plan)
	if !slices.Contains(names, "review") {
		t.Errorf("plan %v does not carry the self-review playbook", names)
	}
	if slices.Contains(names, "independent-review") {
		t.Errorf("plan %v carries the independent reviewer into a code dispatch", names)
	}
}

// TestReviewExitGateRefusesUntilEveryFindingIsDispositioned is the termination
// condition exercised end to end against the shipped protocol: the close
// refuses while the disposition ask is unanswered, names the step it is waiting
// on, and passes once the cap has answered — here with the one finding
// `accepted`, the disposition with no representation anywhere but a sentence.
func TestReviewExitGateRefusesUntilEveryFindingIsDispositioned(t *testing.T) {
	f := newFixture(t, t.TempDir())
	withShipped(t, f)
	t.Setenv("HOME", t.TempDir())

	brief := runDispatch(t, f, dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "review the landed change",
	})
	hc := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}

	// Unanswered, the dispatch cannot close. The refusal names the playbook and
	// the step, which is the vocabulary fs close already uses for a plan it
	// cannot account for — and it is a refusal no flag answers: --confirm is the
	// answer, not a bypass of the question.
	resp := f.close(hc, dispatch.CloseRequest{
		NodeID:   brief.CapNodeID,
		Decision: "the review is done",
	})
	if resp.OK {
		t.Fatalf("close passed with the disposition ask unanswered")
	}
	if !strings.Contains(resp.Error, "independent-review step") {
		t.Errorf("refusal does not name the playbook the step is in: %s", resp.Error)
	}
	if !strings.Contains(resp.Error, "every finding is dispositioned") {
		t.Errorf("refusal names no undispositioned step: %s", resp.Error)
	}

	resp = f.close(hc, dispatch.CloseRequest{
		NodeID: brief.CapNodeID,
		Decision: "1 finding, accepted: the check-specs probe duplicates the exit gate's own " +
			"check — out of scope for this card",
		Confirm: true,
	})
	if !resp.OK {
		t.Fatalf("close refused a fully dispositioned review: %s", resp.Error)
	}
}
