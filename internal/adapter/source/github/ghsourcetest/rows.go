// Package ghsourcetest holds the GitHub plugin's synthetic rows: one per
// situation kind, viewer's or others' pull request, in or out of scope, muted
// or not, each derived by prrow's real deriver.
package ghsourcetest

import (
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/turn"
)

const (
	Viewer   = "octocat"
	Other    = "alice"
	Reviewer = "bob"
	Team     = "acme/core"
)

// Now is the instant every row is derived at.
var Now = time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func ago(d time.Duration) time.Time { return Now.Add(-d) }

// Case is one named row.
type Case struct {
	Name string
	Row  api.Row
}

// Situation is what one row derives from.
type Situation struct {
	PR       dinv.PR
	Events   []dinv.Event
	Override dinv.Override
	Turn     *prrow.TurnFact
	Pending  *round.Pending
	// At is when the row is derived; zero is Now.
	At time.Time
}

// Rows is every case's row.
func Rows() []api.Row {
	cs := Cases()
	out := make([]api.Row, len(cs))
	for i, c := range cs {
		out[i] = c.Row
	}
	return out
}

// Cases is the differential table: every base situation by author, scope and mute.
func Cases() []Case {
	var out []Case
	n := 0
	for _, b := range bases() {
		for _, author := range []string{Viewer, Other} {
			for _, inScope := range []bool{true, false} {
				for _, muted := range []bool{false, true} {
					if muted && b.closed {
						continue
					}
					n++
					s := b.build(Open(n, author))
					if !inScope {
						s.PR.Reasons = nil
					}
					if muted {
						s.Override.MutedUntil = Now.Add(5 * time.Hour)
						s.add(dinv.KindMuted, Viewer, ago(time.Hour), "")
					}
					out = append(out, Case{Name: caseName(b.name, author, inScope, muted), Row: Derive(s)})
				}
			}
		}
	}
	return out
}

func caseName(base, author string, inScope, muted bool) string {
	name := base
	if author == Viewer {
		name += "/mine"
	} else {
		name += "/theirs"
	}
	if inScope {
		name += "/in"
	} else {
		name += "/out"
	}
	if muted {
		name += "/muted"
	}
	return name
}

// Ref is the case pull request n.
func Ref(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }

// Open is an open, in-scope pull request n by author with green checks.
func Open(n int, author string) dinv.PR {
	return dinv.PR{Ref: Ref(n), Title: fmt.Sprintf("Change %d", n), Author: author, State: "OPEN", HeadOid: "h1",
		HeadBranch: "feat/x", BaseBranch: "main", URL: Ref(n).URL(), CreatedAt: ago(2 * day), Reasons: []string{"involved"}, ChecksState: "SUCCESS"}
}

// Event is an event of pull request n.
func Event(n int, kind, actor string, at time.Time, data string) dinv.Event {
	return dinv.Event{PR: Ref(n), Kind: kind, Key: kind + at.Format(time.RFC3339), Actor: actor, At: at, Data: data}
}

// ReviewWorkspace is a review workspace of the loop.
var ReviewWorkspace = dinv.Workspace{ID: "ws-r", DisplayName: "review", Status: "in-review", IsReview: true}

// Derive is s's row as prrow's deriver serves it at s.At (default Now) to Viewer.
func Derive(s Situation) api.Row {
	l, err := turn.Bundled()
	if err != nil {
		panic(fmt.Sprintf("ghsourcetest: bundled intents: %v", err))
	}
	at := s.At
	if at.IsZero() {
		at = Now
	}
	d := prrow.Deriver{Viewer: Viewer, Teams: []string{Team}, Now: at, Stalled: config.Defaults().GitHub.Stalled, Loaded: l, Cfg: config.Defaults(),
		GateOn: true, BriefOn: true, Pending: map[unit.Unit]round.Pending{}, Turns: map[pr.Ref]prrow.TurnFact{}}
	if s.Pending != nil {
		d.Pending[s.PR.Ref.Unit()] = *s.Pending
	}
	if s.Turn != nil {
		d.Turns[s.PR.Ref] = *s.Turn
	}
	s.Override.PR = s.PR.Ref
	return d.Row(s.PR, s.Events, s.Override)
}

type base struct {
	name   string
	closed bool
	edit   func(s *Situation)
}

func (b base) build(p dinv.PR) Situation {
	s := Situation{PR: p}
	b.edit(&s)
	return s
}

func (s *Situation) add(kind, actor string, at time.Time, data string) {
	s.Events = append(s.Events, Event(s.PR.Ref.Number, kind, actor, at, data))
}

// reviewTurn is a review turn in state; an asked or stopped one holds its question, as an answerable turn's
// facts do.
func reviewTurn(state string) *prrow.TurnFact {
	f := &prrow.TurnFact{Facts: situation.TurnFacts{Intent: "review", Role: "round.review", State: state, Summary: "which branch?", At: ago(30 * time.Minute)}, Session: "s1"}
	if state != "started" {
		f.Question = &api.QuestionView{State: state, RecordID: 1, Kind: "choice", Text: "which branch?"}
	}
	return f
}

func bases() []base {
	return []base{
		{name: "merged", closed: true, edit: func(s *Situation) {
			s.PR.State, s.PR.MergedAt = "MERGED", ago(day)
			s.add(dinv.KindMerged, Reviewer, ago(day), "")
		}},
		{name: "closed", closed: true, edit: func(s *Situation) {
			s.PR.State, s.PR.ClosedAt = "CLOSED", ago(day)
			s.add(dinv.KindClosed, s.PR.Author, ago(day), "")
		}},
		{name: "held", edit: func(s *Situation) {
			s.PR.Workspace = ReviewWorkspace
			s.add(dinv.KindRoundHeld, "", ago(time.Hour), "cap reached")
		}},
		{name: "blocked", edit: func(s *Situation) { s.add(dinv.KindRoundBlocked, Viewer, ago(2*time.Hour), "no access") }},
		{name: "to_address", edit: func(s *Situation) { s.add(dinv.KindWouldAddress, "", ago(3*time.Hour), "bob asked") }},
		{name: "round_pending", edit: func(s *Situation) {
			s.PR.ReviewRequests, s.PR.Workspace = []string{Viewer}, ReviewWorkspace
			s.Pending = &round.Pending{Intent: "review", Reason: "new push", Since: ago(time.Minute), Deadline: Now.Add(4 * time.Minute)}
		}},
		{name: "loop_working", edit: func(s *Situation) { s.PR.Workspace, s.Turn = ReviewWorkspace, reviewTurn("started") }},
		{name: "loop_waiting", edit: func(s *Situation) { s.PR.Workspace, s.Turn = ReviewWorkspace, reviewTurn("asked") }},
		{name: "loop_stopped", edit: func(s *Situation) { s.PR.Workspace, s.Turn = ReviewWorkspace, reviewTurn("failed") }},
		{name: "draft", edit: func(s *Situation) { s.PR.IsDraft = true }},
		{name: "checks", edit: func(s *Situation) {
			s.PR.ChecksState = "FAILURE"
			s.PR.FailingChecks = []dinv.FailingCheck{{Name: "lint"}, {Name: "unit"}}
		}},
		{name: "changes_by_viewer", edit: func(s *Situation) { s.add(dinv.KindReview, Viewer, ago(3*time.Hour), "CHANGES_REQUESTED:h1") }},
		{name: "changes_by_other", edit: func(s *Situation) { s.add(dinv.KindReview, Reviewer, ago(3*time.Hour), "CHANGES_REQUESTED:h1") }},
		{name: "merge", edit: func(s *Situation) {
			s.PR.ReviewDecision = "APPROVED"
			s.add(dinv.KindReview, Reviewer, ago(2*time.Hour), "APPROVED:h1")
		}},
		{name: "conflicts", edit: func(s *Situation) {
			s.PR.ReviewDecision, s.PR.Mergeable, s.PR.MergeableAt = "APPROVED", "CONFLICTING", ago(time.Hour)
		}},
		{name: "reply_to_viewer", edit: func(s *Situation) { s.add(dinv.KindComment, Viewer, ago(3*time.Hour), "") }},
		{name: "reply_to_other", edit: func(s *Situation) { s.add(dinv.KindComment, Reviewer, ago(3*time.Hour), "") }},
		{name: "reviewer_back_viewer", edit: func(s *Situation) {
			s.add(dinv.KindComment, Viewer, ago(4*time.Hour), "")
			s.add(dinv.KindComment, s.PR.Author, ago(3*time.Hour), "")
		}},
		{name: "reviewer_back_other", edit: func(s *Situation) {
			s.add(dinv.KindComment, Reviewer, ago(4*time.Hour), "")
			s.add(dinv.KindComment, s.PR.Author, ago(3*time.Hour), "")
		}},
		{name: "review_of_viewer", edit: func(s *Situation) {
			s.PR.ReviewRequests = []string{Viewer}
			s.add(dinv.KindReviewRequest, s.PR.Author, ago(3*time.Hour), Viewer)
		}},
		{name: "review_of_team", edit: func(s *Situation) { s.PR.ReviewRequests = []string{"team:" + Team} }},
		{name: "review_of_other", edit: func(s *Situation) { s.PR.ReviewRequests = []string{Reviewer} }},
		{name: "no_reviewer", edit: func(s *Situation) {}},
		{name: "stalled_review_of_other", edit: func(s *Situation) {
			s.PR.ReviewRequests, s.PR.CreatedAt = []string{Reviewer}, ago(10*day)
			s.add(dinv.KindReviewRequest, s.PR.Author, ago(10*day), Reviewer)
		}},
		{name: "stalled_changes_by_viewer", edit: func(s *Situation) {
			s.PR.CreatedAt = ago(10 * day)
			s.add(dinv.KindReview, Viewer, ago(8*day), "CHANGES_REQUESTED:h1")
		}},
		{name: "approved_by_viewer", edit: func(s *Situation) {
			s.PR.Workspace, s.PR.ReviewRequests = ReviewWorkspace, []string{Reviewer}
			s.add(dinv.KindReview, Viewer, ago(day), "APPROVED:h1")
		}},
	}
}
