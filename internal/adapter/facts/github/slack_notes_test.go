package factsgithub_test

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSlackNotesCostGitHubNothing(t *testing.T) {
	w := newWorld()
	r := w.requested(1, nil)
	log := change.NewLog(change.DefaultWindow, nil)
	p := w.providerWith(log)
	before := facts(t, p, r.Unit())
	rows := w.rows[r]
	thread := unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000001.000100"}
	log.Note(port.TopicSourceEvents, thread)
	ctx := context.Background()
	revs, err := p.Revs(ctx, []unit.Unit{thread})
	if err != nil || len(revs) != 0 {
		t.Errorf("Revs of a Slack unit = %v %v", revs, err)
	}
	after, err := p.Facts(ctx, r.Unit())
	if err != nil {
		t.Fatal(err)
	}
	if len(w.recorded) != 0 || !slices.Equal(w.rows[r].Reasons, rows.Reasons) || !w.rows[r].UpdatedAt.Equal(rows.UpdatedAt) {
		t.Errorf("a Slack note changed the pull request: recorded %v", w.recorded)
	}
	if after["pr.fingerprint"] != before["pr.fingerprint"] {
		t.Errorf("a Slack note changed the pull request's facts")
	}
}
