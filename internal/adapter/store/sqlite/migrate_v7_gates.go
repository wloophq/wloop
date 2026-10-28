package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// v7Gates adds the gates and the gate an answer answers, with one gate for every draft, pending plan item,
// start block, message and item proposal of version 6. A message's gate keeps the message id as its subject;
// an item proposal its ledger message mirrors is that message's gate.
func v7Gates(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE gates (
  id INTEGER PRIMARY KEY,
  task_id TEXT REFERENCES tasks (id),
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, intents TEXT NOT NULL, intent TEXT NOT NULL DEFAULT '', answer TEXT NOT NULL DEFAULT '',
  by_actor TEXT NOT NULL DEFAULT '', rule TEXT NOT NULL DEFAULT '',
  deadline TEXT NOT NULL DEFAULT '', default_intent TEXT NOT NULL DEFAULT '',
  opened_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT ''
)`,
		`CREATE INDEX gates_due ON gates (status, deadline)`,
		`CREATE INDEX gates_task ON gates (task_id)`,
		`CREATE INDEX gates_unit ON gates (provider, unit_key)`,
		`ALTER TABLE turn_answers ADD COLUMN gate_id INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	m := gateMigration{tx: tx, mirrored: map[int64]bool{}}
	for _, s := range []struct {
		name string
		up   func(context.Context) error
	}{{"drafts", m.drafts}, {"plan items", m.planItems}, {"messages", m.messages}, {"item proposals", m.proposals}} {
		if err := s.up(ctx); err != nil {
			return fmt.Errorf("the gates of the %s: %w", s.name, err)
		}
	}
	if err := v7DraftDelivery(ctx, tx); err != nil {
		return fmt.Errorf("the drafts' delivery: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE turn_answers SET gate_id = coalesce((SELECT g.id FROM gates g
		WHERE g.kind IN (?, ?, ?) AND g.subject = CAST(turn_answers.message_id AS TEXT)), 0) WHERE message_id != 0`,
		string(gate.KindQuestion), string(gate.KindPost), string(gate.KindProposal)); err != nil {
		return fmt.Errorf("the gates of the answers: %w", err)
	}
	return nil
}

// v7DraftDelivery leaves a draft only its delivery, once its gate holds its approval: the approval states
// become the delivery ones, and the approval's columns go.
func v7DraftDelivery(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`UPDATE drafts SET state = CASE state WHEN 'proposed' THEN 'held' WHEN 'approved' THEN 'queued' WHEN 'discarded' THEN 'dropped'
  ELSE state END`,
		`ALTER TABLE drafts DROP COLUMN approval_mode`,
		`ALTER TABLE drafts DROP COLUMN approval_window`,
		`ALTER TABLE drafts DROP COLUMN deadline`,
		`ALTER TABLE drafts DROP COLUMN would_policy`,
		`ALTER TABLE drafts DROP COLUMN would_reason`,
		`ALTER TABLE drafts DROP COLUMN would_outcome`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

// v6Gates are the gate of each kind and its intents as the bundled gates.toml of version 7 names them: the store
// cannot read the profile, so a migrated gate carries these, as history of version 6. yes, no and dismiss are the
// intents that approve, refuse and dismiss.
var v6Gates = map[gate.Kind]struct {
	name             string
	intents          []string
	yes, no, dismiss string
}{
	gate.KindDraft:    {"draft", []string{"approve", "discard"}, "approve", "discard", ""},
	gate.KindPlan:     {"plan", []string{"run", "skip"}, "run", "skip", ""},
	gate.KindQuestion: {"question", []string{"answer", "dismiss"}, "answer", "", "dismiss"},
	gate.KindPost:     {"post", []string{"reply", "dismiss"}, "reply", "", "dismiss"},
	gate.KindProposal: {"proposal", []string{"approve", "reject"}, "approve", "reject", ""},
	gate.KindTrust:    {"trust", []string{"trust", "dismiss"}, "trust", "", "dismiss"},
}

// The words version 6 stored, as it wrote them.
const (
	v6StartBlocked  = "round blocked: "
	v6TurnStart     = "turn.start"
	v6LedgerOrigin  = "ledger"
	v6AcceptAnswer  = "accept"
	v6RejectAnswer  = "reject"
	v6DryRun        = "dry_run"
	v6Released      = "released"
	v6DelayApproval = "delay"
	v6RunDefault    = "run"
	windowRule      = "window"
)

// v6Withdrawn are the reasons a workflow withdrew a draft it proposed.
var v6Withdrawn = []string{"assigned", "moot"}

type gateMigration struct {
	tx *sql.Tx
	// mirrored are the item proposals whose ledger message has the gate.
	mirrored map[int64]bool
}

func (m *gateMigration) open(ctx context.Context, g gate.Gate, explicit string) error {
	words := v6Gates[g.Kind]
	g.Name, g.Intents = words.name, words.intents
	var err error
	if g.Task, err = m.taskOf(ctx, explicit, g.Unit); err != nil {
		return err
	}
	_, err = insertGate(ctx, m.tx, g)
	return err
}

// taskOf is the task a gate on u belongs to: explicit when that task exists, else the task u is, else the task
// holding u through task_units, else through task_items, else none.
func (m *gateMigration) taskOf(ctx context.Context, explicit string, u unit.Unit) (task.ID, error) {
	var id sql.NullString
	err := m.tx.QueryRowContext(ctx, `SELECT coalesce(
		(SELECT id FROM tasks WHERE id = ?1),
		(SELECT id FROM tasks WHERE ?2 = ?4 AND id = ?3),
		(SELECT task_id FROM task_units WHERE provider = ?2 AND unit_key = ?3 ORDER BY ord, task_id LIMIT 1),
		(SELECT task_id FROM task_items WHERE provider = ?2 AND unit_key = ?3 ORDER BY task_id LIMIT 1))`,
		explicit, string(u.Provider), u.Key, string(unit.Task)).Scan(&id)
	return task.ID(id.String), err
}

func (m *gateMigration) drafts(ctx context.Context) error {
	type row struct {
		id                                             int64
		u                                              unit.Unit
		taskID, mode, deadline, state, reason, outcome string
		created, updated                               string
	}
	var rs []row
	if err := eachRow(ctx, m.tx, `SELECT id, provider, unit_key, task, approval_mode, deadline, state, reason, would_outcome, created_at, updated_at
		FROM drafts ORDER BY id`, nil, func(rows *sql.Rows) error {
		rs = append(rs, row{})
		p := &rs[len(rs)-1]
		return rows.Scan(&p.id, &p.u.Provider, &p.u.Key, &p.taskID, &p.mode, &p.deadline, &p.state, &p.reason, &p.outcome, &p.created, &p.updated)
	}); err != nil {
		return err
	}
	words := v6Gates[gate.KindDraft]
	for _, r := range rs {
		g := gate.Gate{Kind: gate.KindDraft, Unit: r.u, Subject: strconv.FormatInt(r.id, 10), OpenedAt: v6Time(r.created)}
		switch {
		case r.state == "proposed":
			g.Status = gate.Open
			if r.mode == v6DelayApproval {
				g.Deadline, g.Default = v6Time(r.deadline), words.yes
			}
		case r.state == "discarded" && slices.Contains(v6Withdrawn, r.reason):
			g.Status, g.By = gate.Superseded, task.ActorWorkflow
		case r.state == "discarded" && r.reason == v6DryRun:
			g.Status, g.Intent, g.By, g.Rule = gate.Rejected, words.no, task.ActorWorkflow, v6DryRun
		case r.state == "discarded":
			g.Status, g.Intent, g.By = gate.Rejected, words.no, task.ActorPerson
		case r.state == "sent" && r.mode == "":
			g.Status, g.Intent, g.By = gate.Approved, words.yes, task.ActorAgent
		case r.outcome == v6Released && r.mode == v6DelayApproval:
			g.Status, g.Intent, g.By, g.Rule = gate.Approved, words.yes, task.ActorWorkflow, windowRule
		case r.outcome == v6Released:
			g.Status, g.Intent, g.By, g.Rule = gate.Approved, words.yes, task.ActorWorkflow, r.mode
		default:
			g.Status, g.Intent, g.By = gate.Approved, words.yes, task.ActorPerson
		}
		if g.Status != gate.Open {
			g.ClosedAt = v6Time(r.updated)
		}
		if err := m.open(ctx, g, r.taskID); err != nil {
			return err
		}
	}
	return nil
}

// planItems open a plan gate for each pending item, and a trust gate for each round a start block holds.
func (m *gateMigration) planItems(ctx context.Context) error {
	type row struct {
		id                                             string
		u                                              unit.Unit
		decision, reason, steps, outcome, deadline, at string
	}
	var rs []row
	if err := eachRow(ctx, m.tx, `SELECT id, provider, unit_key, decision, reason, steps, default_outcome, deadline, at
		FROM plan_items WHERE decision IN ('pending', 'blocked') ORDER BY at, id`, nil, func(rows *sql.Rows) error {
		rs = append(rs, row{})
		p := &rs[len(rs)-1]
		return rows.Scan(&p.id, &p.u.Provider, &p.u.Key, &p.decision, &p.reason, &p.steps, &p.outcome, &p.deadline, &p.at)
	}); err != nil {
		return err
	}
	for _, r := range rs {
		g := gate.Gate{Kind: gate.KindPlan, Unit: r.u, Subject: r.id, Status: gate.Open, OpenedAt: v6Time(r.at)}
		if r.decision == "blocked" {
			starts, err := startsATurn(r.steps)
			if err != nil {
				return fmt.Errorf("plan item %s: %w", r.id, err)
			}
			if !starts || !strings.HasPrefix(r.reason, v6StartBlocked) {
				continue
			}
			g.Kind = gate.KindTrust
		} else if r.outcome == v6RunDefault {
			g.Deadline, g.Default = v6Time(r.deadline), v6Gates[gate.KindPlan].yes
		}
		if err := m.open(ctx, g, ""); err != nil {
			return err
		}
	}
	return nil
}

func startsATurn(steps string) (bool, error) {
	var ss []struct {
		Use string `json:"use"`
	}
	if err := json.Unmarshal([]byte(steps), &ss); err != nil {
		return false, err
	}
	return slices.ContainsFunc(ss, func(s struct {
		Use string `json:"use"`
	}) bool {
		return s.Use == v6TurnStart
	}), nil
}

// v6MessageKinds are the gate kinds of the message kinds; a message of a kind version 6 never wrote is a post.
var v6MessageKinds = map[string]gate.Kind{"question": gate.KindQuestion, "agent_post": gate.KindPost, "proposal": gate.KindProposal}

func (m *gateMigration) messages(ctx context.Context) error {
	type row struct {
		id, proposal                        int64
		u                                   unit.Unit
		taskID, kind, origin, state, answer string
		proposalState                       sql.NullString
		created, closed                     string
	}
	var rs []row
	if err := eachRow(ctx, m.tx, `SELECT m.id, m.provider, m.unit_key, m.task_id, m.kind, m.origin_kind, m.state, m.answer, m.proposal,
		p.state, m.created_at, m.closed_at
		FROM messages m LEFT JOIN item_proposals p ON m.origin_kind = ? AND p.id = m.proposal ORDER BY m.id`, []any{v6LedgerOrigin}, func(rows *sql.Rows) error {
		rs = append(rs, row{})
		p := &rs[len(rs)-1]
		return rows.Scan(&p.id, &p.u.Provider, &p.u.Key, &p.taskID, &p.kind, &p.origin, &p.state, &p.answer, &p.proposal,
			&p.proposalState, &p.created, &p.closed)
	}); err != nil {
		return err
	}
	for _, r := range rs {
		kind, ok := v6MessageKinds[r.kind]
		if !ok {
			kind = gate.KindPost
		}
		words := v6Gates[kind]
		g := gate.Gate{Kind: kind, Unit: r.u, Subject: strconv.FormatInt(r.id, 10), OpenedAt: v6Time(r.created), ClosedAt: v6Time(r.closed)}
		ledger := r.origin == v6LedgerOrigin
		switch {
		case ledger && r.proposalState.Valid:
			m.mirrored[r.proposal] = true
			g.Status, g.Intent, g.By = proposalStatus(r.proposalState.String)
		case r.state == "open":
			g.Status = gate.Open
		case r.state == "superseded":
			g.Status, g.By = gate.Superseded, task.ActorWorkflow
		case ledger && r.answer == v6AcceptAnswer:
			g.Status, g.Intent, g.By = gate.Approved, words.yes, task.ActorPerson
		case ledger && (r.answer == v6RejectAnswer || r.state == "dismissed"):
			g.Status, g.Intent, g.By = gate.Rejected, words.no, task.ActorPerson
		case r.state == "dismissed":
			g.Status, g.Intent, g.By = gate.Dismissed, words.dismiss, task.ActorPerson
		case kind == gate.KindProposal:
			g.Status, g.Answer, g.By = gate.Answered, r.answer, task.ActorPerson
		default:
			g.Status, g.Intent, g.Answer, g.By = gate.Answered, words.yes, r.answer, task.ActorPerson
		}
		switch {
		case g.Status == gate.Open:
			g.ClosedAt = time.Time{}
		case g.ClosedAt.IsZero():
			g.ClosedAt = g.OpenedAt
		}
		if err := m.open(ctx, g, r.taskID); err != nil {
			return err
		}
	}
	return nil
}

// proposalStatus is the gate of an item proposal in state.
func proposalStatus(state string) (gate.Status, string, task.Actor) {
	words := v6Gates[gate.KindProposal]
	switch state {
	case "open":
		return gate.Open, "", ""
	case "accepted":
		return gate.Approved, words.yes, task.ActorPerson
	default:
		return gate.Rejected, words.no, task.ActorPerson
	}
}

// proposals open a gate for each item proposal no ledger message mirrors.
func (m *gateMigration) proposals(ctx context.Context) error {
	type row struct {
		id        int64
		u         unit.Unit
		state, at string
	}
	var rs []row
	if err := eachRow(ctx, m.tx, `SELECT id, provider, unit_key, state, at FROM item_proposals ORDER BY id`, nil, func(rows *sql.Rows) error {
		rs = append(rs, row{})
		p := &rs[len(rs)-1]
		return rows.Scan(&p.id, &p.u.Provider, &p.u.Key, &p.state, &p.at)
	}); err != nil {
		return err
	}
	for _, r := range rs {
		if m.mirrored[r.id] {
			continue
		}
		g := gate.Gate{Kind: gate.KindProposal, Unit: r.u, Subject: gate.ProposalSubject(r.id), OpenedAt: v6Time(r.at)}
		g.Status, g.Intent, g.By = proposalStatus(r.state)
		if g.Status != gate.Open {
			g.ClosedAt = g.OpenedAt
		}
		if err := m.open(ctx, g, ""); err != nil {
			return err
		}
	}
	return nil
}

// v6Time reads a time version 6 stored in either of its layouts; a value neither reads is the zero time.
func v6Time(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
