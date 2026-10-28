package change

import (
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }

func basePR() *pr.Facts {
	return &pr.Facts{
		Ref:               pr.Ref{Owner: "acme", Repo: "api", Number: 7},
		State:             dgh.StateOpen,
		Author:            "alice",
		AuthorAssociation: "MEMBER",
		HeadRefOid:        "c2",
		Reviews: []pr.Review{
			{ID: "r1", State: pr.ReviewChangesRequested, Author: "me", SubmittedAt: at(0), CommitOid: "c2"},
		},
		Commits: []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}},
	}
}

func TestReviewer(t *testing.T) {
	fp := at(0).Format(time.RFC3339)
	cases := []struct {
		name       string
		mutate     func(*pr.Facts)
		status     string
		last       string
		owners     []string
		want       ReviewerAction
		wantReason string
	}{
		{name: "nothing new", want: ReviewerNothing, wantReason: "nothing new since my last review"},
		{name: "merged completes", mutate: func(p *pr.Facts) { p.State = dgh.StateMerged }, want: ReviewerComplete},
		{name: "closed completes", mutate: func(p *pr.Facts) { p.State = dgh.StateClosed }, want: ReviewerComplete},
		{name: "merged already completed", mutate: func(p *pr.Facts) { p.State = dgh.StateMerged }, status: "completed", want: ReviewerNothing},
		{name: "draft", mutate: func(p *pr.Facts) {
			p.IsDraft = true
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, want: ReviewerNothing, wantReason: "draft"},
		{name: "no review by me", mutate: func(p *pr.Facts) { p.Reviews = nil }, want: ReviewerNothing},
		{name: "only a pending review by me is no baseline", mutate: func(p *pr.Facts) {
			p.Reviews = []pr.Review{{State: "PENDING", Author: "me", CommitOid: "c2"}}
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, want: ReviewerNothing, wantReason: "no submitted review by me yet"},
		{name: "approved then dismissed by a push", mutate: func(p *pr.Facts) {
			p.Reviews = []pr.Review{{State: pr.ReviewDismissed, Author: "me", SubmittedAt: at(0), CommitOid: "c2"}}
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, want: ReviewerFollowup, wantReason: "new commits since my review"},
		{name: "changes requested then dismissed", mutate: func(p *pr.Facts) {
			p.Reviews = []pr.Review{{State: pr.ReviewDismissed, Author: "me", SubmittedAt: at(0), CommitOid: "c2"}}
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerFollowup, wantReason: "the author commented"},
		{name: "dismissed review with nothing new", mutate: func(p *pr.Facts) {
			p.Reviews = []pr.Review{{State: pr.ReviewDismissed, Author: "me", SubmittedAt: at(0), CommitOid: "c2"}}
		}, want: ReviewerNothing, wantReason: "nothing new since my last review"},
		{name: "head moved", mutate: func(p *pr.Facts) {
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, want: ReviewerFollowup, wantReason: "new commits since my review"},
		{name: "only a base merge", mutate: func(p *pr.Facts) {
			p.HeadRefOid = "m1"
			p.Commits = append(p.Commits, pr.Commit{Oid: "m1", Parents: 2})
		}, want: ReviewerNothing},
		{name: "force push drops the reviewed commit", mutate: func(p *pr.Facts) {
			p.HeadRefOid = "x2"
			p.Commits = []pr.Commit{{Oid: "x1", Parents: 1}, {Oid: "x2", Parents: 1}}
		}, want: ReviewerFollowup},
		{name: "author commented after", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerFollowup, wantReason: "the author commented"},
		{name: "author commented before", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(-5)}}
		}, want: ReviewerNothing},
		{name: "someone else commented", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "bob", CreatedAt: at(5)}}
		}, want: ReviewerNothing},
		{name: "author replied in a thread", mutate: func(p *pr.Facts) {
			p.Reviews = append(p.Reviews, pr.Review{State: pr.ReviewCommented, Author: "alice", SubmittedAt: at(3)})
		}, want: ReviewerFollowup, wantReason: "the author replied in a review"},
		{name: "re-requested", mutate: func(p *pr.Facts) { p.ReviewRequests = []string{"Me"} },
			want: ReviewerFollowup, wantReason: "my review was re-requested"},
		{name: "approved ignores author comments", mutate: func(p *pr.Facts) {
			p.Reviews[0].State = pr.ReviewApproved
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerNothing},
		{name: "approved still follows new commits", mutate: func(p *pr.Facts) {
			p.Reviews[0].State = pr.ReviewApproved
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, want: ReviewerFollowup},
		{name: "newest of my reviews wins", mutate: func(p *pr.Facts) {
			p.Reviews = append(p.Reviews, pr.Review{State: pr.ReviewCommented, Author: "me", SubmittedAt: at(10), CommitOid: "c2"})
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerNothing},
		{name: "one follow-up per review and head", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, last: fp + "@c2", want: ReviewerNothing},
		{name: "a new head after a round that published nothing", mutate: func(p *pr.Facts) {
			p.HeadRefOid = "c3"
			p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
		}, last: fp + "@c2", want: ReviewerFollowup},
		{name: "a fingerprint without the head counts as covered", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, last: fp, want: ReviewerNothing},
		{name: "fork not trusted", mutate: func(p *pr.Facts) {
			p.IsCrossRepository = true
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerNothing},
		{name: "outside contributor not trusted", mutate: func(p *pr.Facts) {
			p.AuthorAssociation = "CONTRIBUTOR"
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, want: ReviewerNothing},
		{name: "owner not allowed", mutate: func(p *pr.Facts) {
			p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
		}, owners: []string{"other"}, want: ReviewerNothing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := basePR()
			if c.mutate != nil {
				c.mutate(p)
			}
			owners := c.owners
			if owners == nil {
				owners = []string{"ACME"}
			}
			got := Reviewer(ReviewerInput{PR: *p, Viewer: "me", WorkspaceStatus: c.status, CompletedStatus: "completed", TrustedOwners: owners, LastFingerprint: c.last})
			if got.Action != c.want {
				t.Fatalf("action = %v (%s), want %v", got.Action, got.Reason, c.want)
			}
			if c.wantReason != "" && got.Reason != c.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, c.wantReason)
			}
			if got.Action == ReviewerFollowup && got.Fingerprint == "" {
				t.Fatal("follow-up without fingerprint")
			}
		})
	}
}

func TestFingerprintIsMyLastReview(t *testing.T) {
	p := basePR()
	p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
	got := Reviewer(ReviewerInput{PR: *p, Viewer: "me", TrustedOwners: []string{"acme"}})
	if want := "2026-10-01T12:00:00Z@c2"; got.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestAReRequestAfterASettledRoundFiresOnce(t *testing.T) {
	p := basePR()
	p.Reviews[0].State = pr.ReviewApproved
	p.ReviewRequests = []string{"me"}
	in := ReviewerInput{PR: *p, Viewer: "me", TrustedOwners: []string{"acme"}, LastFingerprint: "2026-10-01T12:00:00Z@c2"}
	got := Reviewer(in)
	if got.Action != ReviewerFollowup || got.Fingerprint != "2026-10-01T12:00:00Z@c2#rr" {
		t.Fatalf("after a settled round: %v %q (%s)", got.Action, got.Fingerprint, got.Reason)
	}
	in.LastFingerprint = got.Fingerprint
	if again := Reviewer(in); again.Action != ReviewerNothing {
		t.Fatalf("the same re-request fired twice: %s", again.Reason)
	}
}

func TestARoundForAReRequestCoversItsHead(t *testing.T) {
	p := basePR()
	p.Comments = []pr.Comment{{Author: "alice", CreatedAt: at(5)}}
	got := Reviewer(ReviewerInput{PR: *p, Viewer: "me", TrustedOwners: []string{"acme"}, LastFingerprint: "2026-10-01T12:00:00Z@c2#rr"})
	if got.Action != ReviewerNothing {
		t.Fatalf("fired again on the head a re-request round covered: %s", got.Reason)
	}
}

func TestTheRecordedHeadOutweighsADriftedReviewCommit(t *testing.T) {
	drifted := func() *pr.Facts {
		p := basePR()
		p.Reviews[0].State = pr.ReviewApproved
		p.Reviews[0].CommitOid = "m4"
		p.HeadRefOid = "m4"
		p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1}, pr.Commit{Oid: "m4", Parents: 2})
		return p
	}
	in := ReviewerInput{PR: *drifted(), Viewer: "me", TrustedOwners: []string{"acme"}}
	if got := Reviewer(in); got.Action != ReviewerNothing {
		t.Fatalf("without a record GitHub's commit is the baseline: %s", got.Reason)
	}
	in.Recorded = RecordedReview{Head: "c2", At: at(1)}
	if got := Reviewer(in); got.Action != ReviewerFollowup || got.Reason != "new commits since my review" {
		t.Fatalf("the commit before the drifted one was skipped: %v %s", got.Action, got.Reason)
	}
	in.PR = *drifted()
	in.PR.Reviews = append(in.PR.Reviews, pr.Review{State: pr.ReviewApproved, Author: "me", SubmittedAt: at(2), CommitOid: "m4"})
	if got := Reviewer(in); got.Action != ReviewerNothing {
		t.Fatalf("a review newer than the record was measured from the record: %s", got.Reason)
	}
}

func TestAFingerprintWithoutTheHeadIsUpgraded(t *testing.T) {
	p := basePR()
	p.HeadRefOid = "c3"
	p.Commits = append(p.Commits, pr.Commit{Oid: "c3", Parents: 1})
	got := Reviewer(ReviewerInput{PR: *p, Viewer: "me", TrustedOwners: []string{"acme"}, LastFingerprint: at(0).Format(time.RFC3339)})
	if got.Action != ReviewerNothing || got.Fingerprint != "2026-10-01T12:00:00Z@c3" {
		t.Fatalf("got %v %q", got.Action, got.Fingerprint)
	}
}

func TestUntrustedAuthors(t *testing.T) {
	bot := func() *pr.Facts {
		p := basePR()
		p.Author, p.AuthorAssociation = "GitHub-Actions[bot]", "NONE"
		return p
	}
	if Untrusted(*bot(), []string{"acme"}, nil) == "" {
		t.Fatal("a bot with NONE passed without trusted authors")
	}
	if why := Untrusted(*bot(), []string{"acme"}, []string{"github-actions[bot]"}); why != "" {
		t.Fatalf("trusted bot refused: %s", why)
	}
	fork := bot()
	fork.IsCrossRepository = true
	if Untrusted(*fork, []string{"acme"}, []string{"github-actions[bot]"}) == "" {
		t.Fatal("a fork passed")
	}
	if Untrusted(*bot(), []string{"other"}, []string{"github-actions[bot]"}) == "" {
		t.Fatal("the owners allowlist was skipped")
	}
	anonymous := bot()
	anonymous.Author = ""
	if Untrusted(*anonymous, []string{"acme"}, []string{""}) == "" {
		t.Fatal("an empty trusted author matched a pull request with no author")
	}
}

func TestReviewerFollowsTheRemappedCompletedColumn(t *testing.T) {
	merged := basePR()
	merged.State = dgh.StateMerged
	for status, want := range map[string]ReviewerAction{"done": ReviewerNothing, "completed": ReviewerComplete} {
		got := Reviewer(ReviewerInput{PR: *merged, Viewer: "me", WorkspaceStatus: status, CompletedStatus: "done", TrustedOwners: []string{"ACME"}})
		if got.Action != want {
			t.Errorf("status %q: action = %v, want %v", status, got.Action, want)
		}
	}
	if got := Reviewer(ReviewerInput{PR: *merged, Viewer: "me", TrustedOwners: []string{"ACME"}}); got.Action != ReviewerComplete {
		t.Errorf("without a completed column: action = %v", got.Action)
	}
}
