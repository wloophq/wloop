package turns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// results resolves the addresses it lists and records each attach.
type results struct {
	units    map[string]unit.Unit
	attached []attached
	err      error
}

type attached struct {
	entry task.ID
	u     unit.Unit
}

func (r *results) UnitOf(url string) (unit.Unit, bool) {
	u, ok := r.units[url]
	return u, ok
}

func (r *results) Attach(_ context.Context, entry task.ID, u unit.Unit) error {
	r.attached = append(r.attached, attached{entry, u})
	return r.err
}

const (
	prURL  = "https://github.com/acme/api/pull/9"
	docURL = "https://example.com/notes"
)

var (
	brief = task.UnitOf("e-1")
	pr9   = unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
)

func beginService(t *testing.T, attach bool) (*turns.Service, *results) {
	t.Helper()
	s, _, _, _, _ := newService(t)
	s.Intents = dintent.NewRegistry([]dintent.Def{{Name: "begin", UserFacing: true, Provider: dintent.AnyProvider, Session: dintent.Session{Title: "begin"},
		Record: dintent.RecordRules{States: []string{"done", "failed"}, Outcome: []string{"url"}, AttachURL: attach}}})
	r := &results{units: map[string]unit.Unit{prURL: pr9}}
	s.Results = r
	return s, r
}

func beginOn(t *testing.T, s *turns.Service, u unit.Unit) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Send(ctx, turns.SendRequest{Unit: u, Intent: "begin", Turn: "b1", Trigger: turn.ByHand, Records: true}); err != nil {
		t.Fatal(err)
	}
	s.SentTo(ctx, u, "b1", "term_1")
	if _, err := s.Start(ctx, "term_1", "b1"); err != nil {
		t.Fatal(err)
	}
}

func recordBegin(t *testing.T, s *turns.Service, rec record.Record) {
	t.Helper()
	if _, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "b1", Record: rec}); err != nil {
		t.Fatal(err)
	}
}

func TestADoneRecordAttachesItsURLToTheTurnsEntry(t *testing.T) {
	s, r := beginService(t, true)
	beginOn(t, s, brief)
	recordBegin(t, s, record.Record{State: record.Done, Summary: "opened the pull request", Outcome: &record.Outcome{URL: prURL}})
	if len(r.attached) != 1 || r.attached[0] != (attached{"e-1", pr9}) {
		t.Fatalf("attached = %+v, want the pull request on e-1", r.attached)
	}
}

func TestARecordAttachesNothingOutsideItsCase(t *testing.T) {
	for _, c := range []struct {
		name   string
		attach bool
		on     unit.Unit
		rec    record.Record
	}{
		{"the intent does not attach", false, brief, record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{URL: prURL}}},
		{"a failed record", true, brief, record.Record{State: record.Failed, Summary: "s", Resume: "retry", Outcome: &record.Outcome{URL: prURL}}},
		{"an address of no unit", true, brief, record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{URL: docURL}}},
		{"no outcome", true, brief, record.Record{State: record.Done, Summary: "s"}},
		{"a turn on no entry", true, r7, record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{URL: prURL}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, r := beginService(t, c.attach)
			beginOn(t, s, c.on)
			recordBegin(t, s, c.rec)
			if len(r.attached) != 0 {
				t.Fatalf("attached = %+v, want nothing", r.attached)
			}
		})
	}
}

func TestAFailedAttachLeavesTheRecordStored(t *testing.T) {
	s, r := beginService(t, true)
	r.err = errors.New("database is locked")
	beginOn(t, s, brief)
	got, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "b1",
		Record: record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{URL: prURL}}})
	if err != nil || !got.Added || len(r.attached) != 1 {
		t.Fatalf("got %+v, err %v, attached %+v", got, err, r.attached)
	}
}
