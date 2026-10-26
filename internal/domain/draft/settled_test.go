package draft

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSettledAtIsTheNewestSettledDraftOfTheWorkflowOnTheSubject(t *testing.T) {
	t.Parallel()
	thread := unit.Unit{Provider: "chat", Key: "T1/C1/100.1"}
	other := unit.Unit{Provider: "chat", Key: "T1/C1/200.2"}
	entry := unit.Unit{Provider: unit.Entry, Key: "e-0000000000000001"}
	at := func(h int) time.Time { return time.Date(2026, 3, 2, h, 0, 0, 0, time.UTC) }
	mine := func(s State, h int) Draft {
		return Draft{Unit: thread, Entry: "e-0000000000000001", State: s, UpdatedAt: at(h), Origin: Origin{Kind: OriginAuto, Rule: "wf-a", Workflow: "wf-a"}}
	}
	withdrawn := mine(Discarded, 5)
	withdrawn.Reason = WithdrawMoot
	otherWorkflow := mine(Sent, 9)
	otherWorkflow.Origin.Workflow = "wf-b"
	otherUnit := mine(Sent, 9)
	otherUnit.Unit, otherUnit.Entry = other, ""
	ds := []Draft{mine(Sent, 2), withdrawn, mine(Proposed, 8), mine(Approved, 8), mine(Sending, 8), mine(Failed, 8), otherWorkflow, otherUnit}
	for _, c := range []struct {
		name    string
		subject unit.Unit
		ds      []Draft
		want    time.Time
	}{
		{"withdrawn included, open and failed ones not", thread, ds, at(5)},
		{"an entry subject reads the drafts serving it", entry, ds, at(5)},
		{"none settled", thread, []Draft{mine(Proposed, 3)}, time.Time{}},
		{"another unit's drafts", other, ds[:6], time.Time{}},
		{"no drafts", thread, nil, time.Time{}},
	} {
		if got := SettledAt(c.ds, "wf-a", c.subject); !got.Equal(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
