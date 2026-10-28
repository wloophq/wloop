package sqlite

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	gatesThread  = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	gatesPR      = unit.Unit{Provider: "github", Key: "acme/api#7"}
	gatesNowhere = unit.Unit{Provider: "slack", Key: "T9/C9/9.9"}
)

// v6GatesFixture is a version 6 store with entry e-A holding the thread through entry_units, entry e-B holding
// an item of the pull request through entry_items, and a row of every decision waiting on the user.
func v6GatesFixture(t *testing.T) string {
	t.Helper()
	a, b := "e-"+hexA, "e-"+hexB
	return v6Store(t,
		`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES
			('`+a+`', 'action', 'next', 'user', 't', 't', 1), ('`+b+`', 'action', 'next', 'user', 't', 't', 2)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES ('`+a+`', 'slack', 'T1/C1/1.1', 0)`,
		`INSERT INTO entry_items (entry_id, provider, unit_key, item) VALUES ('`+b+`', 'github', 'acme/api#7', 'i1')`,
		`INSERT INTO drafts (id, provider, unit_key, entry, kind, origin_kind, approval_mode, approval_window, deadline, state, reason, would_outcome, created_at, updated_at) VALUES
			(30, 'slack', 'T1/C1/1.1', '', 'reply', 'turn', 'wait', 0, '', 'proposed', '', '', '2026-10-27T09:00:00Z', '2026-10-27T09:00:00Z'),
			(31, 'slack', 'T1/C1/1.1', '`+a+`', 'nudge', 'auto', 'delay', 1800000000000, '2026-10-27T09:30:00.5Z', 'proposed', '', '', '2026-10-27T09:00:00Z', '2026-10-27T09:00:00Z'),
			(32, 'slack', 'T1/C1/1.1', '', 'reply', 'turn', 'wait', 0, '', 'sent', '', 'as_is', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(33, 'slack', 'T1/C1/1.1', '', 'reply', 'turn', 'wait', 0, '', 'discarded', 'user', 'discarded', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(34, 'slack', 'T1/C1/1.1', '', 'nudge', 'auto', 'wait', 0, '', 'discarded', 'moot', 'discarded', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(35, 'slack', 'T9/C9/9.9', '', 'reply', 'turn', 'wait', 0, '', 'proposed', '', '', '2026-10-27T09:00:00Z', '2026-10-27T09:00:00Z'),
			(36, 'slack', 'T1/C1/1.1', '', 'nudge', 'auto', 'delay', 1800000000000, '2026-10-27T09:30:00Z', 'failed', '', 'released', '2026-10-27T09:00:00Z', '2026-10-27T09:30:00Z'),
			(37, 'slack', 'T1/C1/1.1', '', 'nudge', 'auto', 'wait', 0, '', 'discarded', 'nudged by hand', 'discarded', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z')`,
		`INSERT INTO plan_items (id, workflow, provider, unit_key, key_value, decision, reason, steps, default_outcome, deadline, at) VALUES
			('p1', 'review', 'github', 'acme/api#7', 'k', 'pending', 'announced', '[{"use":"turn.start"}]', 'run', '2026-10-28T10:00:00.000000000Z', '2026-10-28T09:00:00.000000000Z'),
			('p2', 'review', 'github', 'acme/api#7', 'k2', 'run', '', '[{"use":"turn.start"}]', '', '', '2026-10-28T09:00:00.000000000Z'),
			('p3', 'review', 'github', 'acme/api#7', 'k3', 'blocked', 'round blocked: trust the worktree', '[{"use":"turn.start"}]', '', '', '2026-10-28T09:00:00.000000000Z'),
			('p4', 'triage', 'slack', 'T1/C1/1.1', 'k4', 'pending', 'waits', '[{"use":"inbox.post"}]', 'wait', '2026-10-28T10:00:00.000000000Z', '2026-10-28T09:00:00.000000000Z'),
			('p5', 'review', 'github', 'acme/api#7', 'k5', 'blocked', 'failed 3 times: boom', '[{"use":"turn.start"}]', '', '', '2026-10-28T09:00:00.000000000Z')`,
		`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, state, sent_at, inputs, runner) VALUES
			(1, 'entry', '`+a+`', 'start', 'r1', 's1', 'asked', '2026-10-27T09:00:00Z', '{}', '')`,
		`INSERT INTO sessions (session_id, manager, agent, pane, workspace_id, worktree, started_at, provider, unit_key) VALUES
			('s1', 'orca', 'claude', 'p1', 'ws1', '/work/api-1', '2026-10-27T09:00:00Z', 'entry', '`+a+`')`,
		`INSERT INTO messages (id, entry_id, provider, unit_key, kind, text, origin_kind, turn_seq, session, proposal, state, answer, created_at, closed_at) VALUES
			(10, '`+a+`', 'entry', '`+a+`', 'question', 'Ship it?', 'turn', 1, 's1', 0, 'open', '', '2026-10-27T09:00:00Z', ''),
			(12, '`+a+`', 'slack', 'T1/C1/1.1', 'question', 'Which branch?', 'turn', 0, '', 0, 'answered', 'main', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(13, '', 'slack', 'T1/C1/1.1', 'agent_post', 'Bruno asked', 'agent', 0, '', 0, 'dismissed', '', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(14, '', 'github', 'acme/api#7', 'proposal', 'Proposed done of item i1', 'ledger', 0, '', 40, 'answered', 'accept', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z'),
			(15, '', 'slack', 'T1/C1/1.1', 'agent_post', 'Done', 'agent', 0, '', 0, 'superseded', '', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z')`,
		`INSERT INTO item_proposals (id, provider, unit_key, item, op, provenance, dedupe, state, at) VALUES
			(40, 'github', 'acme/api#7', 'i1', '{}', 'agent', 'd40', 'accepted', '2026-10-27T09:00:00Z'),
			(41, 'github', 'acme/api#7', 'i1', '{}', 'agent', 'd41', 'open', '2026-10-27T09:00:00Z'),
			(42, 'slack', 'T9/C9/9.9', 'i2', '{}', 'agent', 'd42', 'rejected', '2026-10-27T09:00:00Z')`,
		`INSERT INTO turn_answers (turn_seq, provider, unit_key, at, text, source, route, session, message_id) VALUES
			(1, 'entry', '`+a+`', '2026-10-27T10:00:00Z', 'Yes, ship it', 'cockpit', 'queued', 's1', 10),
			(0, 'slack', 'T1/C1/1.1', '2026-10-27T10:00:00Z', 'main', 'cockpit', 'delivered', '', 12),
			(0, 'slack', 'T1/C1/1.1', '2026-10-27T10:00:00Z', 'no message', 'cockpit', 'delivered', '', 0)`,
	)
}

// migratedGates is the store of v6GatesFixture at version 7, its path and its gates.
func migratedGates(t *testing.T) (*DB, string, []gate.Gate) {
	t.Helper()
	path := v6GatesFixture(t)
	db := migrateV6(t, path)
	all, err := db.Gates(nil).Where(context.Background(), gates.Query{Statuses: []gate.Status{gate.Open, gate.Approved, gate.Rejected,
		gate.Answered, gate.Dismissed, gate.Superseded}})
	if err != nil {
		t.Fatal(err)
	}
	return db, path, all
}

func gateOf(t *testing.T, all []gate.Gate, k gate.Kind, subject string) gate.Gate {
	t.Helper()
	i := slices.IndexFunc(all, func(g gate.Gate) bool { return g.Kind == k && g.Subject == subject })
	if i < 0 {
		t.Fatalf("no %s gate on %s in %+v", k, subject, all)
	}
	if n := len(slices.DeleteFunc(slices.Clone(all), func(g gate.Gate) bool { return g.Kind != k || g.Subject != subject })); n != 1 {
		t.Fatalf("%d %s gates on %s", n, k, subject)
	}
	return all[i]
}

var (
	taskA = task.ID("t-" + hexA)
	taskB = task.ID("t-" + hexB)
)

func TestEveryPendingDraftHasAnOpenGate(t *testing.T) {
	t.Parallel()
	_, _, all := migratedGates(t)
	for _, c := range []struct {
		subject string
		status  gate.Status
		intent  string
		by      task.Actor
		rule    string
		closed  string
		task    task.ID
	}{
		{"30", gate.Open, "", "", "", "", taskA},
		{"31", gate.Open, "", "", "", "", taskA},
		{"32", gate.Approved, "approve", task.ActorPerson, "", "2026-10-27T10:00:00Z", taskA},
		{"33", gate.Rejected, "discard", task.ActorPerson, "", "2026-10-27T10:00:00Z", taskA},
		{"34", gate.Superseded, "", task.ActorWorkflow, "", "2026-10-27T10:00:00Z", taskA},
		{"36", gate.Approved, "approve", task.ActorWorkflow, "window", "2026-10-27T09:30:00Z", taskA},
		{"37", gate.Rejected, "discard", task.ActorPerson, "", "2026-10-27T10:00:00Z", taskA},
	} {
		g := gateOf(t, all, gate.KindDraft, c.subject)
		if g.Status != c.status || g.Intent != c.intent || g.By != c.by || g.Rule != c.rule || g.Task != c.task ||
			g.Name != "draft" || g.Unit != gatesThread || !slices.Equal(g.Intents, []string{"approve", "discard"}) {
			t.Errorf("draft %s: %+v", c.subject, g)
		}
		if want := mustTime(t, "2026-10-27T09:00:00Z"); !g.OpenedAt.Equal(want) {
			t.Errorf("draft %s opened at %v", c.subject, g.OpenedAt)
		}
		if c.closed != "" && !g.ClosedAt.Equal(mustTime(t, c.closed)) || c.closed == "" && !g.ClosedAt.IsZero() {
			t.Errorf("draft %s closed at %v", c.subject, g.ClosedAt)
		}
	}
}

func TestADelayDraftCarriesItsDeadline(t *testing.T) {
	t.Parallel()
	db, _, all := migratedGates(t)
	g := gateOf(t, all, gate.KindDraft, "31")
	if want := mustTime(t, "2026-10-27T09:30:00.5Z"); !g.Deadline.Equal(want) || g.Default != "approve" {
		t.Fatalf("the delay draft's gate %+v", g)
	}
	if w := gateOf(t, all, gate.KindDraft, "30"); !w.Deadline.IsZero() || w.Default != "" {
		t.Fatalf("a wait draft's gate %+v", w)
	}
	due, err := db.Gates(nil).Due(context.Background(), mustTime(t, "2026-10-27T09:30:00.5Z"))
	if err != nil || len(due) != 1 || due[0].ID != g.ID {
		t.Fatalf("due at its deadline %+v %v", due, err)
	}
	if due, _ := db.Gates(nil).Due(context.Background(), mustTime(t, "2026-10-27T09:30:00.4Z")); len(due) != 0 {
		t.Fatalf("due before its deadline %+v", due)
	}
}

func TestAPendingRoundCarriesItsWindow(t *testing.T) {
	t.Parallel()
	_, _, all := migratedGates(t)
	g := gateOf(t, all, gate.KindPlan, "p1")
	if g.Status != gate.Open || g.Name != "plan" || g.Unit != gatesPR || g.Task != taskB || g.Default != "run_now" ||
		!g.Deadline.Equal(mustTime(t, "2026-10-28T10:00:00Z")) || !g.OpenedAt.Equal(mustTime(t, "2026-10-28T09:00:00Z")) ||
		!slices.Equal(g.Intents, []string{"run_now", "skip", "guide"}) {
		t.Fatalf("the pending round's gate %+v", g)
	}
	if w := gateOf(t, all, gate.KindPlan, "p4"); w.Status != gate.Open || !w.Deadline.IsZero() || w.Default != "" || w.Task != taskA {
		t.Fatalf("a pending item that waits %+v", w)
	}
	trust := gateOf(t, all, gate.KindTrust, "p3")
	if trust.Status != gate.Open || trust.Name != "trust" || trust.Unit != gatesPR || trust.Task != taskB || !slices.Equal(trust.Intents, []string{"trust_run", "unblock"}) {
		t.Fatalf("the start block's gate %+v", trust)
	}
	for _, g := range all {
		if g.Subject == "p2" || g.Subject == "p5" {
			t.Errorf("a plan item no one waits on has a gate %+v", g)
		}
	}
}

func TestAnsweredQuestionsMigrateAsAnsweredGates(t *testing.T) {
	t.Parallel()
	_, path, all := migratedGates(t)
	q := gateOf(t, all, gate.KindQuestion, "12")
	if q.Status != gate.Answered || q.Intent != "answer" || q.Answer != "main" || q.By != task.ActorPerson || q.Task != taskA ||
		q.Name != "question" || !slices.Equal(q.Intents, []string{"answer", "dismiss"}) || !q.ClosedAt.Equal(mustTime(t, "2026-10-27T10:00:00Z")) {
		t.Fatalf("the answered question's gate %+v", q)
	}
	if p := gateOf(t, all, gate.KindPost, "13"); p.Status != gate.Dismissed || p.Intent != "dismiss" || p.Task != taskA || !slices.Equal(p.Intents, []string{"reply", "dismiss"}) {
		t.Fatalf("the dismissed post's gate %+v", p)
	}
	if p := gateOf(t, all, gate.KindPost, "15"); p.Status != gate.Superseded || p.Intent != "" {
		t.Fatalf("the superseded post's gate %+v", p)
	}
	if got := strings1(t, rawDB(t, path), `SELECT gate_id FROM turn_answers WHERE message_id = 12`); !slices.Equal(got, []string{itoa(q.ID)}) {
		t.Errorf("the answer of message 12 names gate %q, want %d", got, q.ID)
	}
	if got := strings1(t, rawDB(t, path), `SELECT gate_id FROM turn_answers WHERE message_id = 0`); !slices.Equal(got, []string{"0"}) {
		t.Errorf("an answer of no message names gate %q", got)
	}
}

func TestAnOpenQuestionKeepsItsRouteThroughV7Gates(t *testing.T) {
	t.Parallel()
	db, path, all := migratedGates(t)
	ctx := context.Background()
	tk := task.UnitOf(taskA)
	q := gateOf(t, all, gate.KindQuestion, "10")
	if q.Status != gate.Open || q.Task != taskA || q.Unit != tk {
		t.Fatalf("the open question's gate %+v", q)
	}
	if got := strings1(t, rawDB(t, path), `SELECT gate_id || ' ' || session || ' ' || route FROM turn_answers WHERE message_id = 10`); !slices.Equal(got, []string{itoa(q.ID) + " s1 queued"}) {
		t.Fatalf("the queued answer %q", got)
	}
	var session, state string
	if err := db.R().QueryRowContext(ctx, `SELECT session, state FROM messages WHERE id = ?`, q.Subject).Scan(&session, &state); err != nil || session != "s1" || state != "open" {
		t.Fatalf("the gate's subject reads message %s on %q (%s): %v", q.Subject, session, state, err)
	}
	books, err := New(db).Books(ctx, tk)
	if err != nil {
		t.Fatal(err)
	}
	b := books[tk]
	queued := slices.IndexFunc(b.Answers, func(a dturn.Answer) bool { return a.Route == dturn.Queued })
	if len(b.Turns) != 1 || b.Turns[0].Session != "s1" || queued < 0 || b.Answers[queued].Session != "s1" || b.Answers[queued].TurnSeq != 1 {
		t.Fatalf("the turn and answer the question routes through: %+v %+v", b.Turns, b.Answers)
	}
}

func TestItemProposalsBecomeGates(t *testing.T) {
	t.Parallel()
	_, _, all := migratedGates(t)
	mirrored := gateOf(t, all, gate.KindProposal, "14")
	if mirrored.Status != gate.Approved || mirrored.Intent != "approve" || mirrored.By != task.ActorPerson || mirrored.Task != taskB ||
		mirrored.Name != "proposal" || !slices.Equal(mirrored.Intents, []string{"approve", "reject"}) {
		t.Fatalf("the proposal its message mirrors %+v", mirrored)
	}
	if open := gateOf(t, all, gate.KindProposal, gate.ProposalSubject(41)); open.Status != gate.Open || open.Task != taskB || open.Unit != gatesPR {
		t.Fatalf("the open proposal %+v", open)
	}
	if rejected := gateOf(t, all, gate.KindProposal, gate.ProposalSubject(42)); rejected.Status != gate.Rejected || rejected.Intent != "reject" || rejected.By != task.ActorPerson {
		t.Fatalf("the rejected proposal %+v", rejected)
	}
	for _, g := range all {
		if g.Subject == gate.ProposalSubject(40) {
			t.Errorf("proposal 40 has a second gate %+v", g)
		}
	}
}

func TestAGateOnAUnitWithNoTaskHasANullTask(t *testing.T) {
	t.Parallel()
	_, path, all := migratedGates(t)
	for _, g := range []gate.Gate{gateOf(t, all, gate.KindDraft, "35"), gateOf(t, all, gate.KindProposal, gate.ProposalSubject(42))} {
		if g.Task != "" || g.Unit != gatesNowhere {
			t.Errorf("gate %+v", g)
		}
	}
	if got := strings1(t, rawDB(t, path), `SELECT count(*) FROM gates WHERE task_id IS NULL`); !slices.Equal(got, []string{"2"}) {
		t.Errorf("%q gates with a NULL task, want 2", got)
	}
	if got := strings1(t, rawDB(t, path), `SELECT count(*) FROM gates WHERE task_id = ''`); !slices.Equal(got, []string{"0"}) {
		t.Errorf("%q gates with an empty task", got)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestAMigratedDraftKeepsOnlyItsDelivery(t *testing.T) {
	t.Parallel()
	path := v6GatesFixture(t)
	rawExec(t, path, `INSERT INTO drafts (id, provider, unit_key, kind, origin_kind, approval_mode, state, evidence_ref, created_at, updated_at, sending_at) VALUES
		(38, 'slack', 'T1/C1/1.1', 'reply', 'turn', 'wait', 'approved', '', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z', ''),
		(39, 'slack', 'T1/C1/1.1', 'reply', 'turn', 'wait', 'sending', '', '2026-10-27T09:00:00Z', '2026-10-27T10:00:00Z', '2026-10-27T10:00:00Z')`)
	migrateV6(t, path)
	raw := rawDB(t, path)
	want := []string{"30 held", "31 held", "32 sent", "33 dropped", "34 dropped", "35 held", "36 failed", "37 dropped", "38 queued", "39 sending"}
	if got := strings1(t, raw, `SELECT id || ' ' || state FROM drafts ORDER BY id`); !slices.Equal(got, want) {
		t.Fatalf("delivery states %q, want %q", got, want)
	}
	if got := strings1(t, raw, `SELECT name FROM pragma_table_info('drafts') WHERE name IN
		('approval_mode', 'approval_window', 'deadline', 'would_policy', 'would_reason', 'would_outcome')`); len(got) != 0 {
		t.Fatalf("columns left: %q", got)
	}
}
