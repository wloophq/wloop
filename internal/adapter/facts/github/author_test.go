package factsgithub_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

func addressed(u unit.Unit) turn.Book {
	t := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Address), ID: "a1", State: turn.Done, Session: "term_1", SentAt: t0}
	return turn.Book{Unit: u, Turns: []turn.Turn{t},
		Records: []turn.Record{{ID: 1, TurnSeq: 1, Hash: "pushed", At: t0, Record: record.Record{State: record.Done, Summary: "pushed"}}}}
}

func TestAuthorFactsDriveTheRealViewsService(t *testing.T) {
	w := newWorld()
	ready := w.add(5, me, func(row *dinv.PR, _ *pr.Facts) {
		row.HeadBranch, row.ReviewDecision, row.ChecksState, row.Mergeable = "ready", "APPROVED", "SUCCESS", "MERGEABLE"
	})
	changes := w.add(6, me, func(row *dinv.PR, f *pr.Facts) {
		row.HeadBranch, row.ReviewDecision = "changes", "CHANGES_REQUESTED"
		f.NewestChangesRequestOid = "head"
	})
	w.workspace(wsdomain.Workspace{ID: "dev5", Path: "/w/ready", DisplayName: "ready", Branch: "ready", LinkedPR: 5})
	w.workspace(wsdomain.Workspace{ID: "dev6", Path: "/w/changes", DisplayName: "changes", Branch: "changes", LinkedPR: 6})
	w.git.heads["/w/ready"] = "head"

	books := turnstest.NewStore()
	sink := testsink.New("board").WithAuthorColumns()
	svc := &views.Service{Books: books, Intents: turnstest.Intents(), State: viewstest.NewState(), Sinks: []views.Sink{{ViewSink: sink, Placements: sink}},
		Clock: clock{w}, Log: t.Logf, Backoff: func(int) time.Duration { return 5 * time.Millisecond }, Facts: w.provider()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	for _, r := range []pr.Ref{ready, changes} {
		books.Put(addressed(r.Unit()))
		sink.Place(r.Unit(), string(view.InReview))
	}
	svc.Changed(ctx, ready.Unit(), changes.Unit())

	deadline := time.Now().Add(5 * time.Second)
	for {
		c5, m5, _ := sink.Card(ready.Unit())
		c6, m6, unread := sink.Card(changes.Unit())
		if c5 == string(view.Ready) && c6 == string(view.InProgress) {
			if m5 != "approved and checks pass" || m6 != "changes requested" || !unread {
				t.Fatalf("cards = (%q, %q), (%q, %q, unread %v)", c5, m5, c6, m6, unread)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cards = %q (%q), %q (%q)", c5, m5, c6, m6)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAuthorFactsSayWhichWorkspaceTheBoardHas(t *testing.T) {
	w := newWorld()
	mine := w.add(5, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "mine" })
	reviewed := w.add(6, "alice", func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "theirs" })
	loose := w.add(7, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "loose" })
	due := w.add(8, me, func(row *dinv.PR, f *pr.Facts) {
		row.HeadBranch, row.ReviewDecision = "due", "CHANGES_REQUESTED"
		f.Reviews = []pr.Review{{ID: "b1", State: pr.ReviewChangesRequested, Author: "bob", SubmittedAt: t0.Add(-time.Hour), CommitOid: "head", HasContent: true}}
		f.NewestChangesRequestOid = "head"
	})
	w.workspace(wsdomain.Workspace{ID: "dev5", Path: "/w/mine", DisplayName: "mine", Branch: "mine", LinkedPR: 5})
	w.workspace(wsdomain.Workspace{ID: "rev6", Path: "/w/rev6", DisplayName: "REVIEW theirs", Branch: "theirs", LinkedPR: 6})
	w.workspace(wsdomain.Workspace{ID: "dev8", Path: "/w/due", DisplayName: "due", Branch: "due", LinkedPR: 8})
	colleague := w.add(9, "alice", func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "hers" })
	w.workspace(wsdomain.Workspace{ID: "dev9", Path: "/w/hers", DisplayName: "hers", Branch: "hers"})
	p := w.provider()
	for _, c := range []struct {
		ref            pr.Ref
		author, review bool
	}{{mine, true, false}, {reviewed, false, true}, {loose, false, false}, {due, false, false}, {colleague, false, false}} {
		f, err := p.Author(context.Background(), c.ref.Unit(), false)
		if err != nil || f == nil {
			t.Fatalf("%s: %+v %v", c.ref, f, err)
		}
		if f.AuthorWorkspace != c.author || f.ReviewWorkspace != c.review {
			t.Errorf("%s: author %v review %v", c.ref, f.AuthorWorkspace, f.ReviewWorkspace)
		}
	}
}

func TestAPartialListingMovesNoCardWhoseAddressRoundItHides(t *testing.T) {
	w := newWorld()
	due := w.add(8, me, func(row *dinv.PR, f *pr.Facts) {
		row.HeadBranch, row.ReviewDecision = "due", "CHANGES_REQUESTED"
		f.Reviews = []pr.Review{{ID: "b1", State: pr.ReviewChangesRequested, Author: "bob", SubmittedAt: t0.Add(-time.Hour), CommitOid: "head", HasContent: true}}
		f.NewestChangesRequestOid = "head"
	})
	w.workspace(wsdomain.Workspace{ID: "dev8", Path: "/w/due", DisplayName: "due", Branch: "due", LinkedPR: 8})
	w.snap.WorkspaceScope.Complete = false
	f, err := w.provider().Author(context.Background(), due.Unit(), false)
	if err != nil || f == nil {
		t.Fatalf("%+v %v", f, err)
	}
	if f.AuthorWorkspace {
		t.Fatal("a partial listing let the facts move a card whose address round is due")
	}
}

func TestTheBoardReadsOnlyTheCardedPullRequestsAndAtOnce(t *testing.T) {
	w := newWorld()
	mine := w.add(5, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "mine" })
	reviewed := w.add(6, "alice", func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "theirs" })
	loose := w.add(7, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "loose" })
	w.add(10, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch, row.State = "gone", "MERGED" })
	w.workspace(wsdomain.Workspace{ID: "dev5", Path: "/w/mine", DisplayName: "mine", Branch: "mine", LinkedPR: 5})
	w.workspace(wsdomain.Workspace{ID: "rev6", Path: "/w/rev6", DisplayName: "REVIEW theirs", Branch: "theirs", LinkedPR: 6})
	p := w.providerWith(change.NewLog(change.DefaultWindow, nil))
	ctx := context.Background()
	carded, err := p.Carded(ctx)
	if err != nil || !slices.Equal(carded, []unit.Unit{mine.Unit(), reviewed.Unit()}) {
		t.Fatalf("Carded = %v, %v", carded, err)
	}
	before := w.factReads
	if err := p.Prefetch(ctx, append(carded, loose.Unit())); err != nil {
		t.Fatal(err)
	}
	for _, r := range []pr.Ref{mine, reviewed, loose} {
		if _, err := p.Author(ctx, r.Unit(), false); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.factReads - before; got != 1 {
		t.Fatalf("%d facts queries, want 1", got)
	}
}
