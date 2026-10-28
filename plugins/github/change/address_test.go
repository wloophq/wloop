package change

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestReviewsToAddress(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 11, h, 0, 0, 0, time.UTC) }
	review := func(id, state, author, oid string, h int, content bool) pr.Review {
		return pr.Review{ID: id, State: state, Author: author, CommitOid: oid, SubmittedAt: at(h), HasContent: content}
	}
	base := func(reviews ...pr.Review) *pr.Facts {
		return &pr.Facts{State: pr.StateOpen, Author: "me", HeadRefOid: "head", Reviews: reviews}
	}
	withThreads := func(p *pr.Facts, ts ...pr.Thread) *pr.Facts {
		p.OpenThreads = ts
		return p
	}
	cases := []struct {
		name    string
		pr      *pr.Facts
		handled time.Time
		skip    []string
		want    []string
		fp      string
	}{
		{name: "changes requested on the head", pr: base(review("a", "CHANGES_REQUESTED", "bob", "head", 10, true)), want: []string{"bob"}, fp: "2026-10-11T10:00:00Z"},
		{name: "bot comments count", pr: base(review("a", "COMMENTED", "lintbot-ai", "head", 10, true)), want: []string{"lintbot-ai"}, fp: "2026-10-11T10:00:00Z"},
		{name: "empty approval", pr: base(review("a", "APPROVED", "bob", "head", 10, false))},
		{name: "approval with nits", pr: base(review("a", "APPROVED", "bob", "head", 10, true)), want: []string{"bob"}, fp: "2026-10-11T10:00:00Z"},
		{name: "my own reply", pr: base(review("a", "COMMENTED", "me", "head", 10, true))},
		{name: "skipped reviewer", pr: base(review("a", "COMMENTED", "copilot", "head", 10, true)), skip: []string{"Copilot"}},
		{name: "first sight ignores reviews of older heads", pr: base(review("a", "COMMENTED", "bob", "old", 10, true))},
		{name: "after a round, newer reviews of any head", pr: base(review("a", "COMMENTED", "bob", "old", 9, true), review("b", "COMMENTED", "bob", "old", 11, true)), handled: at(10), want: []string{"bob"}, fp: "2026-10-11T11:00:00Z"},
		{name: "open thread from an older head", pr: withThreads(base(review("a", "CHANGES_REQUESTED", "bot", "old", 9, true)), pr.Thread{LastAuthor: "bot", LastAt: at(9)}), want: []string{"bot"}, fp: "2026-10-11T09:00:00Z"},
		{name: "open thread I answered last", pr: withThreads(base(), pr.Thread{LastAuthor: "me", LastAt: at(9)})},
		{name: "open thread older than the round", pr: withThreads(base(), pr.Thread{LastAuthor: "bob", LastAt: at(9)}), handled: at(10)},
		{name: "reply in an open thread after the round", pr: withThreads(base(), pr.Thread{LastAuthor: "bob", LastAt: at(11)}), handled: at(10), want: []string{"bob"}, fp: "2026-10-11T11:00:00Z"},
		{name: "nothing newer than the round", pr: base(review("a", "COMMENTED", "bob", "head", 10, true)), handled: at(10)},
		{name: "merged", pr: &pr.Facts{State: pr.StateMerged, HeadRefOid: "head", Reviews: []pr.Review{review("a", "COMMENTED", "bob", "head", 10, true)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ids, fp := ReviewsToAddress(AddressInput{PR: *c.pr, Viewer: "me", Handled: c.handled, SkipReviewers: c.skip})
			if len(ids) != len(c.want) || fp != c.fp {
				t.Fatalf("got %v %q, want %v %q", ids, fp, c.want, c.fp)
			}
			for i := range ids {
				if ids[i] != c.want[i] {
					t.Fatalf("got %v, want %v", ids, c.want)
				}
			}
		})
	}
}

func TestFailingChecks(t *testing.T) {
	ok := pr.Check{Name: "build"}
	sonar := pr.Check{Name: "SonarCloud", Failed: true}
	cases := []struct {
		name    string
		state   string
		checks  []pr.Check
		handled string
		ignore  []string
		want    []string
	}{
		{name: "all green", checks: []pr.Check{ok}},
		{name: "a failure", checks: []pr.Check{ok, sonar, sonar}, want: []string{"SonarCloud"}},
		{name: "still running", checks: []pr.Check{sonar, {Name: "build", Pending: true}}},
		{name: "head already handled", checks: []pr.Check{sonar}, handled: "h1"},
		{name: "ignored failure", checks: []pr.Check{ok, sonar}, ignore: []string{"sonarcloud"}},
		{name: "ignored check still running", checks: []pr.Check{sonar, {Name: "slow", Pending: true}}, ignore: []string{"slow"}, want: []string{"SonarCloud"}},
		{name: "closed", state: pr.StateMerged, checks: []pr.Check{sonar}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := c.state
			if state == "" {
				state = pr.StateOpen
			}
			p := &pr.Facts{State: state, HeadRefOid: "h1", Checks: c.checks}
			if got := FailingChecks(*p, c.handled, c.ignore); !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
