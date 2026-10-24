//go:build live

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

const (
	roundWait = 20 * time.Minute
	// addressByHandAfter is how long S4 waits for an automatic address round
	// before it asks for one by hand.
	addressByHandAfter = 10 * time.Minute
)

// sandbox is one TestLiveSandbox run: the pull requests it opened, which
// its cleanup closes along with every workspace linked to them.
type sandbox struct {
	l   *live
	prs []pr.Ref
	// answered is a review chain whose held draft an answer through the API
	// published, for S5 to check.
	answered *answeredChain
}

type answeredChain struct {
	url string
	seq int64
	// asked is the turn whose question the answer optionID went to.
	asked    int64
	optionID string
}

func ref(n int) pr.Ref {
	owner, repo, _ := strings.Cut(Sandbox, "/")
	return pr.Ref{Owner: owner, Repo: repo, Number: n}
}

// TestLiveSandbox runs the review and address loops on the sandbox, from
// GitHub to a recorded turn, triggered by the sandbox's bot workflows.
func TestLiveSandbox(t *testing.T) {
	s := &sandbox{l: newLive(t)}
	t.Cleanup(func() { s.cleanup(t) })
	var s1 pr.Ref
	t.Run("S1 first round", func(t *testing.T) { s1 = s.firstRound(t) })
	if t.Failed() {
		return
	}
	t.Run("S2 follow-up with history", func(t *testing.T) { s.followUp(t, s1) })
	t.Run("S3 the deleted workspace", func(t *testing.T) { s.deletedWorkspace(t, s1) })
	t.Run("S4 address rounds", s.addressRounds)
	t.Run("S5 a held question answered through the API", s.heldQuestion)
	t.Run("S6 an answer to a busy agent is typed when it stops", s.busyAnswer)
	t.Run("S7 address --self-review", s.selfReview)
}

func (s *sandbox) cleanup(t *testing.T) {
	ctx := context.Background()
	s.adoptRunPRs(t)
	for _, r := range s.prs {
		if err := SandboxOnly(r.Owner + "/" + r.Repo); err != nil {
			t.Error(err)
			continue
		}
		if _, err := try("", "gh", "pr", "close", strconv.Itoa(r.Number), "-R", Sandbox, "--delete-branch"); err != nil {
			t.Logf("closing %s: %v", r, err)
		}
		if _, err := try("", "gh", brand.Name, "remove", r.URL(), "--force"); err != nil {
			t.Logf(brand.Name+" remove %s: %v", r, err)
		}
	}
	all, err := s.l.orca.List(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	repo := s.l.orcaRepo(t, Sandbox)
	for _, ws := range all {
		mine := strings.HasPrefix(ws.DisplayName, Prefix+s.l.stamp)
		linked := ws.RepoID == repo.ID && slices.ContainsFunc(s.prs, func(r pr.Ref) bool { return r.Number == ws.LinkedPR })
		if mine || linked {
			s.l.removeWorkspace(t, repo.Path, ws)
		}
	}
}

// adoptRunPRs adds to s.prs every open sandbox pull request titled with this
// run's prefix that a failed subtest did not get to record.
func (s *sandbox) adoptRunPRs(t *testing.T) {
	prefix := s.l.name("")
	out, err := try("", "gh", "pr", "list", "-R", Sandbox, "--state", "open", "--limit", "100", "--json", "number,title")
	if err != nil {
		t.Logf("listing the run's pull requests: %v", err)
		return
	}
	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		t.Logf("listing the run's pull requests: %v", err)
		return
	}
	for _, p := range prs {
		r := ref(p.Number)
		if strings.HasPrefix(p.Title, prefix) && !slices.Contains(s.prs, r) {
			s.prs = append(s.prs, r)
		}
	}
}

// openBotPR has the sandbox's bot open a pull request titled title and
// returns it once GitHub lists it.
func (s *sandbox) openBotPR(t *testing.T, title string) pr.Ref {
	t.Helper()
	if err := MustOwn(title); err != nil {
		t.Fatal(err)
	}
	ghWrite(t, Sandbox, "workflow", "run", "bot-pr.yml", "-f", "title="+title)
	var found pr.Ref
	waitFor(t, 5*time.Minute, "the bot opens "+title, func() (bool, string) {
		n, ok := prByTitle(t, title)
		if ok {
			found = ref(n)
			s.prs = append(s.prs, found)
		}
		return ok, "not listed yet"
	})
	return found
}

var pullURLNumber = regexp.MustCompile(`/pull/(\d+)`)

func prByTitle(t *testing.T, title string) (int, bool) {
	t.Helper()
	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(ghRead(t, "pr", "list", "-R", Sandbox, "--state", "open", "--search", title+" in:title", "--json", "number,title"), &prs); err != nil {
		t.Fatal(err)
	}
	for _, p := range prs {
		if p.Title == title {
			return p.Number, true
		}
	}
	return 0, false
}

// sweepEvery is how often sweepUntil asks for a sweep; the book is read on
// every poll.
const sweepEvery = time.Minute

// sweepUntil reads the book every poll and asks for a sweep at most once a
// minute, until cond holds.
func sweepUntil(t *testing.T, r pr.Ref, timeout time.Duration, what string, cond func(liveBook) (bool, string)) liveBook {
	t.Helper()
	var last liveBook
	var watched time.Time
	waitFor(t, timeout, what, func() (bool, string) {
		if time.Since(watched) >= sweepEvery {
			watched = time.Now()
			if err := client.New(config.DaemonSocket()).Trigger(context.Background(), "sweep"); err != nil {
				t.Logf("sweep: %v", err)
			}
		}
		last = bookOf(t, r.URL())
		return cond(last)
	})
	return last
}

// newTurn is the newest turn of intent sent after seq.
func newTurn(b liveBook, intent turn.Intent, after int64) (turn.Turn, bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		if x := b.Turns[i]; x.Intent == intent && x.Seq > after && x.State != turn.Withdrawn {
			return x, true
		}
	}
	return turn.Turn{}, false
}

// lastRecord is the newest record of turn seq or of a turn resuming it.
func lastRecord(b liveBook, seq int64) (turn.Record, bool) {
	chain := map[int64]bool{seq: true}
	for _, x := range b.Turns {
		if chain[x.ResumeOf] {
			chain[x.Seq] = true
		}
	}
	for i := len(b.Records) - 1; i >= 0; i-- {
		if r := b.Records[i]; chain[r.TurnSeq] {
			return r, true
		}
	}
	return turn.Record{}, false
}

// option is the option an answer picks: the one that publishes, else the
// recommended one, else the first.
func option(q *record.Question) string {
	if q == nil || len(q.Options) == 0 {
		return ""
	}
	for _, o := range q.Options {
		if o.Publishes {
			return o.ID
		}
	}
	for _, o := range q.Options {
		if o.ID == q.Recommend {
			return o.ID
		}
	}
	return q.Options[0].ID
}

// settle waits for turn seq's chain to end; a question on the way is
// answered through the API from source. It returns the final record and
// whether a held review draft was answered.
func (s *sandbox) settle(t *testing.T, r pr.Ref, seq int64, source turn.Source) (turn.Record, bool) {
	t.Helper()
	answeredDraft := false
	var final turn.Record
	waitFor(t, roundWait, fmt.Sprintf("turn %d of %s ends", seq, r), func() (bool, string) {
		b := bookOf(t, r.URL())
		rec, ok := lastRecord(b, seq)
		if !ok {
			return false, fmt.Sprintf("turns %+v", b.Turns)
		}
		switch rec.Record.State {
		case record.Done, record.Settled:
			final = rec
			return true, ""
		case record.Failed:
			t.Fatalf("turn %d failed: %s", seq, rec.Record.Summary)
		case record.Asked:
			if slices.ContainsFunc(b.Answers, func(a turn.Answer) bool { return a.TurnSeq == rec.TurnSeq && a.At.After(rec.At) }) {
				return false, "answered, waiting for the next record"
			}
			id := option(rec.Record.Question)
			if id == "" {
				t.Fatalf("turn %d asks without options: %+v", seq, rec.Record)
			}
			if rec.Record.Artifact != nil && rec.Record.Artifact.Kind == "review-draft" {
				answeredDraft = true
				s.answered = &answeredChain{url: r.URL(), seq: seq, asked: rec.TurnSeq, optionID: id}
			}
			s.l.answer(t, r, id, source)
		}
		return false, "state " + string(rec.Record.State)
	})
	return final, answeredDraft
}

func reviewURLs(t *testing.T, n int) []string {
	t.Helper()
	out := ghRead(t, "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", Sandbox, n), "--jq", ".[].html_url")
	return strings.Fields(string(out))
}

func (s *sandbox) firstRound(t *testing.T) pr.Ref {
	r := s.openBotPR(t, s.l.name("s1"))
	b := sweepUntil(t, r, roundWait, brand.Name+" sends the first review round", func(b liveBook) (bool, string) {
		_, ok := newTurn(b, turn.Intent(dintent.Review), 0)
		return ok, fmt.Sprintf("turns %+v", b.Turns)
	})
	first, _ := newTurn(b, turn.Intent(dintent.Review), 0)
	if first.Trigger != turn.Automated {
		t.Fatalf("the first round's trigger is %s", first.Trigger)
	}
	rec, _ := s.settle(t, r, first.Seq, turn.TUI)
	if rec.Record.State != record.Done || rec.Record.Outcome == nil || rec.Record.Outcome.Verdict == "" {
		t.Fatalf("the first round ended %+v", rec.Record)
	}
	if !slices.Contains(reviewURLs(t, r.Number), rec.Record.Outcome.URL) {
		t.Fatalf("%s is not a review of %s", rec.Record.Outcome.URL, r)
	}
	return r
}

func (s *sandbox) followUp(t *testing.T, r pr.Ref) {
	before := bookOf(t, r.URL())
	last, _ := newTurn(before, turn.Intent(dintent.Review), 0)
	s1, _ := lastRecord(before, last.Seq)
	ghWrite(t, Sandbox, "workflow", "run", "bot-push.yml", "-f", "pr="+strconv.Itoa(r.Number))
	b := sweepUntil(t, r, roundWait, brand.Name+" sends a follow-up review", func(b liveBook) (bool, string) {
		_, ok := newTurn(b, turn.Intent(dintent.Review), last.Seq)
		return ok, fmt.Sprintf("turns %+v", b.Turns)
	})
	follow, _ := newTurn(b, turn.Intent(dintent.Review), last.Seq)
	if follow.Trigger != turn.Automated {
		t.Fatalf("the follow-up's trigger is %s", follow.Trigger)
	}
	text := b.instructions(follow.Round)
	for _, want := range []string{"### Earlier turns", s1.Record.Summary, "endorsed:", "#### Follow-up approval rule"} {
		if !strings.Contains(text, want) {
			t.Errorf("the follow-up's instructions lack %q", want)
		}
	}
	s.settle(t, r, follow.Seq, turn.TUI)
}

// reviewWorkspace is the sandbox workspace linked to the pull request.
func (s *sandbox) reviewWorkspace(t *testing.T, r pr.Ref) (wsdomain.Workspace, bool) {
	t.Helper()
	all, err := s.l.orca.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	repo := s.l.orcaRepo(t, Sandbox)
	for _, ws := range all {
		if ws.RepoID == repo.ID && ws.LinkedPR == r.Number {
			return ws, true
		}
	}
	return wsdomain.Workspace{}, false
}

func (s *sandbox) deletedWorkspace(t *testing.T, r pr.Ref) {
	old, ok := s.reviewWorkspace(t, r)
	if !ok {
		t.Fatalf("no review workspace of %s", r)
	}
	s.l.removeWorkspace(t, s.l.orcaRepo(t, Sandbox).Path, old)
	if _, listed := s.l.workspaceByID(t, old.ID); listed {
		t.Fatalf("workspace %s is still listed after its removal", old.ID)
	}
	before := bookOf(t, r.URL())
	last, _ := newTurn(before, turn.Intent(dintent.Review), 0)
	ghWrite(t, Sandbox, "workflow", "run", "bot-push.yml", "-f", "pr="+strconv.Itoa(r.Number))
	b := sweepUntil(t, r, roundWait, brand.Name+" sends the follow-up in a new workspace and types it into a session", func(b liveBook) (bool, string) {
		follow, ok := newTurn(b, turn.Intent(dintent.Review), last.Seq)
		return ok && follow.SentTo != "", fmt.Sprintf("turns %+v", b.Turns)
	})
	follow, _ := newTurn(b, turn.Intent(dintent.Review), last.Seq)
	if _, ok := s.reviewWorkspace(t, r); !ok {
		t.Fatalf("no workspace linked to %s after the follow-up", r)
	}
	if follow.SentTo == "" {
		t.Fatalf("the follow-up turn %d records no session handle", follow.Seq)
	}
	for _, x := range b.Turns {
		if x.Intent != turn.Intent(dintent.Review) || x.Seq >= follow.Seq {
			continue
		}
		if x.SentTo == follow.SentTo {
			t.Errorf("the follow-up went to session %s of the earlier turn %d, not a new one", x.SentTo, x.Seq)
		}
		if rec, ok := lastRecord(b, x.Seq); ok && !strings.Contains(b.instructions(follow.Round), rec.Record.Summary) {
			t.Errorf("the instructions lack the earlier round %q", rec.Record.Summary)
		}
	}
	if len(b.events("would_review")) != 0 {
		t.Error("the follow-up was only observed: would_review")
	}
	s.settle(t, r, follow.Seq, turn.TUI)
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "--", name)
	run(t, dir, "git", "commit", "-m", message)
}

// onBranch says whether the pull request's head branch holds path.
func onBranch(branch, path string) bool {
	_, err := try("", "gh", "api", fmt.Sprintf("repos/%s/contents/%s?ref=%s", Sandbox, path, url.QueryEscape(branch)), "--jq", ".name")
	return err == nil
}

// addressTurn waits for an address turn after seq, asking for one by hand
// once the automatic one is late.
func addressTurn(t *testing.T, r pr.Ref, after int64) turn.Turn {
	t.Helper()
	deadline := time.Now().Add(addressByHandAfter)
	byHand := false
	b := sweepUntil(t, r, roundWait, brand.Name+" sends an address round", func(b liveBook) (bool, string) {
		if _, ok := newTurn(b, turn.Intent(dintent.Address), after); ok {
			return true, ""
		}
		if !byHand && time.Now().After(deadline) {
			byHand = true
			t.Logf("no automatic address round on %s; asking for one by hand", r)
			if out, err := try("", "gh", brand.Name, "address", r.URL()); err != nil {
				t.Logf("address: %v %s", err, out)
			}
		}
		return false, fmt.Sprintf("turns %+v", b.Turns)
	})
	x, _ := newTurn(b, turn.Intent(dintent.Address), after)
	return x
}

func (s *sandbox) addressRounds(t *testing.T) {
	name := s.l.name("s4")
	ws := s.l.createWorkspace(t, Sandbox, name)
	branch := ws.Branch
	if err := MustOwnBranch(branch); err != nil {
		t.Fatal(err)
	}
	commitFile(t, ws.Path, "hello.txt", "hello\n", name+": add hello.txt")
	gitPush(t, ws.Path, branch)
	created := ghWrite(t, Sandbox, "pr", "create", "--head", branch, "--title", name, "--body", brand.Name+" live test "+name)
	m := pullURLNumber.FindSubmatch(created)
	if m == nil {
		t.Fatalf("gh pr create printed no pull request URL: %s", created)
	}
	n, _ := strconv.Atoi(string(m[1]))
	r := ref(n)
	s.prs = append(s.prs, r)
	if err := MustOwnWorkspace(s.l.guarded(t, ws)); err != nil {
		t.Fatal(err)
	}
	if err := s.l.orca.LinkUnit(context.Background(), ws.ID, r.Unit()); err != nil {
		t.Fatal(err)
	}

	ghWrite(t, Sandbox, "workflow", "run", "bot-comment.yml", "-f", "pr="+strconv.Itoa(n),
		"-f", "body=Please rename hello.txt to greeting.txt", "-f", "event=REQUEST_CHANGES")
	rename := addressTurn(t, r, 0)
	if rec, _ := s.settle(t, r, rename.Seq, turn.TUI); rec.Record.State != record.Done {
		t.Fatalf("the rename round ended %+v", rec.Record)
	}
	waitFor(t, 2*time.Minute, "the head renames hello.txt", func() (bool, string) {
		return onBranch(branch, "greeting.txt") && !onBranch(branch, "hello.txt"), "not renamed on " + branch
	})

	run(t, ws.Path, "git", "pull", "--ff-only")
	commitFile(t, ws.Path, "FAIL", "fail\n", name+": fail the checks")
	gitPush(t, ws.Path, branch)
	fix := addressTurn(t, r, rename.Seq)
	if rec, _ := s.settle(t, r, fix.Seq, turn.TUI); rec.Record.State != record.Done {
		t.Fatalf("the failing-check round ended %+v", rec.Record)
	}
	waitFor(t, 10*time.Minute, "the head drops FAIL and the checks pass", func() (bool, string) {
		if onBranch(branch, "FAIL") {
			return false, "FAIL still on " + branch
		}
		out, err := try("", "gh", "pr", "checks", strconv.Itoa(n), "-R", Sandbox)
		return err == nil, string(out)
	})
}

func (s *sandbox) heldQuestion(t *testing.T) {
	if s.answered != nil {
		b := bookOf(t, s.answered.url)
		if a, ok := b.answer(s.answered.asked, s.answered.optionID); !ok || a.Route != turn.Typed || a.Source != turn.TUI {
			t.Errorf("the earlier answer %s reads %+v, want typed from tui", s.answered.optionID, a)
		}
		if got := b.Endorsed(s.answered.seq); got != turn.EndorsedApproved {
			t.Errorf("the answered automated chain reads endorsed %q, want %q", got, turn.EndorsedApproved)
		}
		return
	}
	r := s.openBotPR(t, s.l.name("s5"))
	command(t, "review", r.URL(), "Do not publish. Record state asked with your drafted review as a review-draft artifact and a choice whose publishing options carry publishes: true.")
	var asked turn.Record
	var seq int64
	waitFor(t, roundWait, "the review asks with its held draft", func() (bool, string) {
		b := bookOf(t, r.URL())
		x, ok := newTurn(b, turn.Intent(dintent.Review), 0)
		if !ok {
			return false, "no review turn"
		}
		seq = x.Seq
		rec, ok := lastRecord(b, x.Seq)
		if ok && rec.Record.State == record.Asked && rec.Record.Question != nil && len(rec.Record.Question.Options) > 0 {
			asked = rec
			return true, ""
		}
		return false, fmt.Sprintf("records %+v", b.Records)
	})
	id := option(asked.Record.Question)
	s.l.answer(t, r, id, turn.TUI)
	var final turn.Record
	waitFor(t, roundWait, "the answered review publishes", func() (bool, string) {
		b := bookOf(t, r.URL())
		if a, ok := b.answer(asked.TurnSeq, id); ok && a.Route != turn.Pending && a.Route != turn.Queued && a.Route != turn.Typed {
			t.Fatalf("the answer went %s, want typed", a.Route)
		}
		rec, ok := lastRecord(b, seq)
		if ok && rec.Record.State == record.Done {
			final = rec
			return true, ""
		}
		return false, fmt.Sprintf("records %+v", b.Records)
	})
	if final.Record.Outcome == nil || !slices.Contains(reviewURLs(t, r.Number), final.Record.Outcome.URL) {
		t.Fatalf("the published review is not on GitHub: %+v", final.Record.Outcome)
	}
	if b := bookOf(t, r.URL()); b.Endorsed(seq) != turn.EndorsedByHand {
		t.Errorf("endorsed %q, want %q", b.Endorsed(seq), turn.EndorsedByHand)
	}
}

func (s *sandbox) busyAnswer(t *testing.T) {
	r := s.openBotPR(t, s.l.name("s6"))
	command(t, "review", r.URL(), "This is a synthetic test of "+brand.Name+": do not review and do not publish anything. "+
		"Call turn_end with state asked and a confirm question (recommend yes, publishes false) asking whether to go on. "+
		"When turn_end answers, do not end your turn yet: first run the Bash command "+busyHold+" in the foreground, not in the background, and wait for it to finish; then end your turn. "+
		"When the user answers, call turn_end with state settled and put the answer, word for word, in the record's user field.")
	var seq int64
	waitFor(t, roundWait, "the review asks", func() (bool, string) {
		b := bookOf(t, r.URL())
		x, ok := newTurn(b, turn.Intent(dintent.Review), 0)
		if !ok {
			return false, "no review turn"
		}
		seq = x.Seq
		rec, ok := lastRecord(b, x.Seq)
		if ok && rec.Record.State == record.Asked {
			return true, ""
		}
		return false, fmt.Sprintf("records %+v", b.Records)
	})
	const note = "synthetic note 7f3a"
	if out := s.l.answerWith(t, r, api.ActionRequest{Yes: true, Guidance: note, Source: string(turn.TUI)}); !strings.Contains(out, api.AnswerQueued) {
		t.Fatalf("the answer to the sleeping agent was not queued: %q", out)
	}
	if a, ok := tuiAnswer(bookOf(t, r.URL()), seq); !ok || a.Route != turn.Queued {
		t.Fatalf("the stored answer reads %+v, want queued", a)
	}
	var final turn.Record
	waitFor(t, roundWait, "the queued answer reaches the agent once it stops", func() (bool, string) {
		b := bookOf(t, r.URL())
		a, ok := tuiAnswer(b, seq)
		if ok && a.Route != turn.Queued && a.Route != turn.Typed {
			t.Fatalf("the queued answer went %s, want typed", a.Route)
		}
		rec, ok := lastRecord(b, seq)
		if ok && a.Route == turn.Typed && rec.Record.State == record.Settled {
			final = rec
			return true, ""
		}
		return false, fmt.Sprintf("answer %+v, records %+v", a, b.Records)
	})
	if !strings.Contains(final.Record.User, note) || !strings.Contains(strings.ToLower(final.Record.User), "yes") {
		t.Errorf("the agent read %q, want yes with the note %q", final.Record.User, note)
	}
}

// busyHold keeps S6's agent busy past the answer: Claude Code blocks a
// foreground `sleep` and kills a Bash command after 120 s.
const busyHold = "`python3 -c 'import time; time.sleep(100)'`"

// tuiAnswer is the turn's answer from the TUI source, leaving out the one
// the loop stores from the record's user field.
func tuiAnswer(b liveBook, seq int64) (turn.Answer, bool) {
	for i := len(b.Answers) - 1; i >= 0; i-- {
		if a := b.Answers[i]; a.TurnSeq == seq && a.Source == turn.TUI {
			return a, true
		}
	}
	return turn.Answer{}, false
}

// shoutWithDefect is S7's change: the plan says shout upper-cases the whole
// greeting, the code leaves the name as given. No check covers it.
const shoutWithDefect = "def shout(name):\n    return \"HELLO, \" + name\n"

var shoutPlan = "Plan: app/shout.py adds shout(name), the greeting of greet.greet in upper case, the name included: shout('x') == 'HELLO, X'. " +
	"This is a " + brand.Name + " live test. After your own review and before changing any code, stop at the gate once: record asked with the triage artifact " +
	"and a confirm question (recommend yes) on whether to apply your fixes. When the user says yes, apply them, push, and finish the round."

var intakeNumber = regexp.MustCompile(`intake (\d+)`)

func (s *sandbox) selfReview(t *testing.T) {
	name := s.l.name("s7")
	ws := s.l.createWorkspace(t, Sandbox, name)
	branch := ws.Branch
	if err := MustOwnBranch(branch); err != nil {
		t.Fatal(err)
	}
	commitFile(t, ws.Path, "app/shout.py", shoutWithDefect, name+": add shout")
	gitPush(t, ws.Path, branch)
	created := ghWrite(t, Sandbox, "pr", "create", "--head", branch, "--title", name,
		"--body", brand.Name+" live test "+name+"\n\nAdds app/shout.py: shout(name) is the greeting in upper case, the name included.")
	m := pullURLNumber.FindSubmatch(created)
	if m == nil {
		t.Fatalf("gh pr create printed no pull request URL: %s", created)
	}
	n, _ := strconv.Atoi(string(m[1]))
	r := ref(n)
	s.prs = append(s.prs, r)
	if err := MustOwnWorkspace(s.l.guarded(t, ws)); err != nil {
		t.Fatal(err)
	}
	if err := s.l.orca.LinkUnit(context.Background(), ws.ID, r.Unit()); err != nil {
		t.Fatal(err)
	}

	for _, intent := range []string{"review", "nudge"} {
		if out, err := try("", "gh", brand.Name, intent, r.URL(), "--self-review"); err == nil || !strings.Contains(err.Error(), "--self-review applies to address") {
			t.Errorf("%s --self-review: %v %s", intent, err, out)
		}
	}

	asked := string(command(t, "address", r.URL(), "--no-wait", "--self-review", shoutPlan))
	first := intakeNumber.FindStringSubmatch(asked)
	if first == nil {
		t.Fatalf("address --no-wait printed no intake: %s", asked)
	}
	if out, err := try("", "gh", brand.Name, "address", r.URL(), "--no-wait"); err == nil {
		t.Logf("intake %s left the queue before the plain ask; the overlap goes unchecked: %s", first[1], out)
		if second := intakeNumber.FindSubmatch(out); second != nil && string(second[1]) != first[1] {
			if _, err := try("", "gh", brand.Name, "intake", "cancel", string(second[1])); err != nil {
				t.Fatalf("cancelling the plain intake %s: %v", second[1], err)
			}
		}
	} else if want := "intake " + first[1] + " already asks for this pull request's address with --self-review"; !strings.Contains(err.Error(), want) {
		if _, started := newTurn(bookOf(t, r.URL()), turn.Intent(dintent.Address), 0); started {
			t.Logf("intake %s became a turn before the plain ask; the overlap goes unchecked: %v", first[1], err)
		} else {
			t.Errorf("the plain ask: %v, want %q", err, want)
		}
	}

	var x turn.Turn
	var gate turn.Record
	waitFor(t, roundWait, "the self-review stops at the gate", func() (bool, string) {
		b := bookOf(t, r.URL())
		var ok bool
		if x, ok = newTurn(b, turn.Intent(dintent.Address), 0); !ok {
			return false, "no address turn"
		}
		if x.Inputs["self_review"] != true {
			t.Fatalf("the address turn's inputs are %v", x.Inputs)
		}
		rec, ok := lastRecord(b, x.Seq)
		if ok && rec.Record.State == record.Failed {
			t.Fatalf("the self-review failed: %s", rec.Record.Summary)
		}
		if ok && rec.Record.State == record.Asked {
			gate = rec
			return true, ""
		}
		return false, fmt.Sprintf("records %+v", b.Records)
	})
	if a := gate.Record.Artifact; a == nil || a.Kind != "triage" || !strings.Contains(a.Body, "(own)") {
		t.Errorf("the gate's artifact does not mark its own findings: %+v", gate.Record.Artifact)
	}
	if !strings.Contains(bookOf(t, r.URL()).instructions(x.Round), "#### Your own review of the change") {
		t.Error("the instructions lack the own review")
	}

	s.l.own(x.SentTo)
	s.l.closeTerminal(t, x.SentTo)
	waitFor(t, time.Minute, "Orca no longer lists the closed terminal", func() (bool, string) {
		return !s.l.listed(t, ws.ID, x.SentTo), x.SentTo + " still listed"
	})
	s.l.answerWith(t, r, api.ActionRequest{Yes: true, Source: string(turn.Cockpit)})
	var resumed turn.Turn
	waitFor(t, roundWait, "a new session resumes the self-review", func() (bool, string) {
		b := bookOf(t, r.URL())
		for _, y := range b.Turns {
			if y.ResumeOf == x.Seq {
				resumed = y
			}
		}
		return resumed.Seq != 0 && resumed.SentTo != "", fmt.Sprintf("turns %+v", b.Turns)
	})
	s.l.own(resumed.SentTo)
	if resumed.Inputs["self_review"] != true {
		t.Errorf("the resumed turn's inputs are %v", resumed.Inputs)
	}
	if text := bookOf(t, r.URL()).instructions(resumed.Round); !strings.Contains(text, "review again only what changed since") {
		t.Error("the resumed instructions review the whole change again")
	}
	if rec, _ := s.settle(t, r, x.Seq, turn.TUI); rec.Record.State != record.Done {
		t.Fatalf("the self-review ended %+v", rec.Record)
	}

	if got := strings.TrimSpace(string(ghRead(t, "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", Sandbox, n), "--jq", "length"))); got != "0" {
		t.Errorf("the self-review published %s reviews", got)
	}
	if got := strings.TrimSpace(string(ghRead(t, "api", fmt.Sprintf("repos/%s/pulls/%d/comments", Sandbox, n), "--jq", "length"))); got != "0" {
		t.Errorf("the self-review published %s inline comments", got)
	}
	var comments []string
	if err := json.Unmarshal(ghRead(t, "api", fmt.Sprintf("repos/%s/issues/%d/comments", Sandbox, n), "--jq", "[.[].body]"), &comments); err != nil {
		t.Fatal(err)
	}
	for _, c := range comments {
		if strings.Contains(strings.TrimSpace(c), "\n") {
			t.Errorf("a top-level comment of a round with only own findings is more than one line:\n%s", c)
		}
	}
	run(t, ws.Path, "git", "pull", "--ff-only")
	run(t, ws.Path, "python3", "-c", "import sys; sys.path.insert(0, 'app'); from shout import shout; assert shout('x') == 'HELLO, X', shout('x')")
	if s.l.listed(t, ws.ID, resumed.SentTo) {
		s.l.closeTerminal(t, resumed.SentTo)
	}
}
