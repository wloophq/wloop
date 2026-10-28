package cli

import (
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/turn"
)

func TestAutomatedReasonJoinsThePlannersNamesWithTheRead(t *testing.T) {
	t.Parallel()
	f := workflow.Facts{"pr.address.reviewers": []string{"ana", "jussara-ai"}, "pr.address.checks": []any{"ci/test", "gone"}}
	checks := []dpr.Check{{Name: "lint"}, {Name: "ci/test", Failed: true, Summary: "3 failed", URL: "https://ci/9"}}
	want := &turn.RoundReason{
		Reviewers: []turn.RoundReviewer{{Login: "ana"}, {Login: "jussara-ai"}},
		Checks:    []turn.FailingCheck{{Name: "ci/test", Summary: "3 failed", URL: "https://ci/9"}, {Name: "gone"}},
	}
	if got := automatedReason(f, dpr.Facts{Checks: checks}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if got := automatedReason(workflow.Facts{}, dpr.Facts{Checks: checks}); got != nil {
		t.Errorf("nothing due gave %+v", got)
	}
}

func TestAutomatedReasonNamesTheBaseOfAConflict(t *testing.T) {
	t.Parallel()
	got := automatedReason(workflow.Facts{"pr.address.conflict": true}, dpr.Facts{BaseRefName: "main"})
	if want := (&turn.RoundReason{Conflict: &turn.RoundConflict{Base: "main"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestHandReasonShowsAConflictMarkedWhenAnEarlierRoundCoveredItsHead(t *testing.T) {
	t.Parallel()
	f := dpr.Facts{State: dgh.StateOpen, HeadRefOid: "h2", BaseRefName: "main", Mergeable: "CONFLICTING"}
	if got := handReason(f, "me", time.Time{}, "", "h1", config.Config{}); got == nil || !reflect.DeepEqual(got.Conflict, &turn.RoundConflict{Base: "main"}) {
		t.Errorf("got %+v", got)
	}
	if got := handReason(f, "me", time.Time{}, "", "h2", config.Config{}); got == nil || !reflect.DeepEqual(got.Conflict, &turn.RoundConflict{Base: "main", Handled: true}) {
		t.Errorf("got %+v", got)
	}
}

func handFacts(at func(h int) time.Time) dpr.Facts {
	return dpr.Facts{State: dgh.StateOpen, Author: "me", HeadRefOid: "h2",
		Reviews: []dpr.Review{
			{Author: "ana", State: dpr.ReviewCommented, HasContent: true, SubmittedAt: at(1), CommitOid: "h2"},
			{Author: "bob", State: dpr.ReviewChangesRequested, HasContent: true, SubmittedAt: at(3), CommitOid: "h2"},
			{Author: "noisy", State: dpr.ReviewCommented, HasContent: true, SubmittedAt: at(3), CommitOid: "h2"},
		},
		Checks: []dpr.Check{{Name: "build", Pending: true}, {Name: "lint", Failed: true, Summary: "unused x"}, {Name: "flaky", Failed: true}},
	}
}

func TestHandReasonMarksWhatAnEarlierRoundCovered(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	cfg := config.Config{Plugins: config.Plugins{GitHub: config.GitHub{AddressSkipReviewers: []string{"noisy"}, IgnoreChecks: []string{"flaky"}}}}
	got := handReason(handFacts(at), "me", at(2), "h2", "", cfg)
	want := &turn.RoundReason{
		Reviewers: []turn.RoundReviewer{{Login: "ana", Handled: true}, {Login: "bob"}},
		Checks:    []turn.FailingCheck{{Name: "lint", Summary: "unused x", Handled: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	got = handReason(handFacts(at), "me", time.Time{}, "h1", "", cfg)
	if got.Reviewers[0].Handled || got.Checks[0].Handled {
		t.Errorf("without an earlier round nothing is handled: %+v", got)
	}
}

func TestHandReasonIsNilWithNothingToShow(t *testing.T) {
	t.Parallel()
	if got := handReason(dpr.Facts{State: dgh.StateOpen, HeadRefOid: "h"}, "me", time.Time{}, "", "", config.Config{}); got != nil {
		t.Errorf("got %+v", got)
	}
}
