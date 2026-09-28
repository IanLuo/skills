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
	"encoding/json"
	"os"
	"os/exec"
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

// ---- the revision a review reads ------------------------------------------
//
// A gate that asks "was this change reviewed?" without asking "at which
// revision?" is satisfied forever by a review that closed before the last three
// commits: it says a review happened, not that it read the code being closed.
// So a review records the revision it read, and the gate compares that against
// the revision the change stands at now.

const reviewEntry = `name: review-entry
applies_when: type=review-task
engine: herdr
agent: pi
entry:
  - check: true
`

// gitRepo makes root a git repository with one commit and returns its revision:
// a project root a revision can be read from.
func gitRepo(t *testing.T, root string) string {
	t.Helper()
	gitIn(t, root, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(root, "seed.txt"), "seed\n")
	gitIn(t, root, "add", "seed.txt")
	return commitIn(t, root, "seed")
}

// commitIn commits whatever is staged and returns the new revision, so a test
// can move a tree past the revision a review read.
func commitIn(t *testing.T, root, message string) string {
	t.Helper()
	gitIn(t, root, "-c", "user.email=fs@test.invalid", "-c", "user.name=fs test", "commit", "-q", "-m", message)
	return gitIn(t, root, "rev-parse", "HEAD")
}

// moveOn writes one file and commits it, returning the new revision.
func moveOn(t *testing.T, root, name string) string {
	t.Helper()
	writeFile(t, filepath.Join(root, name), name+"\n")
	gitIn(t, root, "add", name)
	return commitIn(t, root, name)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// reviewReadsOf reads the revision a review dispatch recorded that it read, out
// of the plan it recorded — the same payload the gate reads.
func reviewReadsOf(t *testing.T, f *fixture, review string) string {
	t.Helper()
	payload, ok, err := f.h.PlanFor("cap", review)
	if err != nil || !ok {
		t.Fatalf("PlanFor %s: ok=%v err=%v", review, ok, err)
	}
	var plan trace.Plan
	if err := json.Unmarshal(payload, &plan); err != nil {
		t.Fatalf("decode plan of %s: %v", review, err)
	}
	return plan.Reads
}

// workAndReview prepares a delivered work dispatch in a git repo and a review
// that reads it, returning their nodes and the revision the review was made at.
func workAndReview(t *testing.T, root string) (*fixture, string, string, string) {
	t.Helper()
	f := newFixture(t, root)
	f.writePlaybook(t, "code-entry", passingEntry)
	f.writePlaybook(t, "review-entry", reviewEntry)

	work := runDispatch(t, f, devReq("the change")).CapNodeID
	review := prepareOnly(t, f, dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "review the change", Reviews: work,
	}).CapNodeID
	return f, work, review, reviewReadsOf(t, f, review)
}

// A review records the revision it read, taken from the reviewed dispatch's own
// tree, and the gate accepts it while the change still stands there.
func TestReviewRecordsTheRevisionItReadsAndTheGateAcceptsIt(t *testing.T) {
	root := t.TempDir()
	rev := gitRepo(t, root)
	f, work, review, reads := workAndReview(t, root)

	if reads != rev {
		t.Errorf("the review recorded %s, want the revision it was prepared at, %s", reads, rev)
	}
	if resp := f.h.TaskUpdate("cap", review, "done", "the review is finished", nil); !resp.OK {
		t.Fatal(resp.Error)
	}

	resp := dispatch.Reviews(f.h, f.reg, work)
	if !resp.OK {
		t.Fatalf("Reviews refused a review of the revision the work stands at: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReviewsResult)
	if result.ReviewedBy != review || result.Reads != rev {
		t.Errorf("result = %+v, want review %s at %s", result, review, rev)
	}
}

// The defect: a review of an earlier state of the change satisfies the gate
// forever. Once the work moves past the revision a review read, the review is
// no longer an answer — and a fresh review of the revision it stands at now is.
func TestReviewsRefusesAReviewTheWorkHasMovedPast(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	f, work, review, reads := workAndReview(t, root)
	if resp := f.h.TaskUpdate("cap", review, "done", "the review is finished", nil); !resp.OK {
		t.Fatal(resp.Error)
	}

	moved := moveOn(t, root, "later.txt")

	resp := dispatch.Reviews(f.h, f.reg, work)
	if resp.OK {
		t.Fatalf("Reviews accepted a review of %s while the work stands at %s", reads, moved)
	}
	for _, want := range []string{review, reads, moved, "--reviews " + work} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}

	// Re-reviewing at the revision the work stands at now is the way past, and
	// it is accepted with no override.
	again := prepareOnly(t, f, dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "review the change again", Reviews: work,
	}).CapNodeID
	if got := reviewReadsOf(t, f, again); got != moved {
		t.Errorf("the second review recorded %s, want %s", got, moved)
	}
	if resp := f.h.TaskUpdate("cap", again, "done", "the second review is finished", nil); !resp.OK {
		t.Fatal(resp.Error)
	}
	if resp := dispatch.Reviews(f.h, f.reg, work); !resp.OK {
		t.Fatalf("Reviews refused a review of the revision the work stands at: %s", resp.Error)
	}
}

// A review that names no revision — every review closed before the field
// existed — is not accepted as a review of the current revision. Silent
// reinterpretation is the one thing the rule may not do, so it refuses and
// names both the re-review and the waiver that records a skip.
func TestReviewsRefusesAReviewThatNamesNoRevision(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	f := newFixture(t, root)
	f.writePlaybook(t, "code-entry", passingEntry)

	work := runDispatch(t, f, devReq("the change")).CapNodeID
	old := addReviewNode(t, f, work)
	if resp := f.h.TaskUpdate("cap", old, "done", "the review is finished", nil); !resp.OK {
		t.Fatal(resp.Error)
	}

	resp := dispatch.Reviews(f.h, f.reg, work)
	if resp.OK {
		t.Fatal("Reviews accepted a done review that names no revision")
	}
	for _, want := range []string{old, "names no revision", "--without-review"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}
}

// A review of a tree fs cannot read a revision from is refused at preparation,
// not prepared with an empty revision: a review that names none can never
// satisfy the gate it exists for, and handing the cap one would be a promise the
// close is certain to break.
func TestPrepareRefusesAReviewOfATreeWithNoRevision(t *testing.T) {
	root := t.TempDir() // a project root that is not a git repository
	f := newFixture(t, root)
	f.writePlaybook(t, "code-entry", passingEntry)
	f.writePlaybook(t, "review-entry", reviewEntry)

	work := prepareOnly(t, f, devReq("the change")).CapNodeID
	recordDelivery(t, f, work, "w1:p1", "w-1")

	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "review the change", Reviews: work,
	})
	if resp.OK {
		t.Fatal("a review with no revision to name must be refused")
	}
	for _, want := range []string{"--reviews " + work, "no revision"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}
}
