package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "Store.UnitEvents", "SegmentStore.Rehoming")
	slackDM := unit.Unit{Provider: "slack", Key: "T000AAA/D000AAA/1700000000.000200"}
	slackSeg := segUnit("T000AAA", "D000AAA", "1700000000000100")
	writes = append(writes,
		writeCase{
			method: "Store.RecordSourceEvents",
			call: func(ctx context.Context, e *noteEnv) error {
				return New(e.db).RecordSourceEvents(ctx, port.Event{Unit: u7, Kind: "push", Key: "abc", At: noteAt})
			},
			topics: port.TopicSourceEvents, units: []unit.Unit{u7},
		},
		writeCase{
			method: "DB.SetMarks",
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.SetMarks(ctx, u7, map[string]string{"review": "abc"})
			},
			topics: port.TopicRounds, units: []unit.Unit{u7},
		},
		writeCase{
			method: "DB.RehomeDrafts",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Drafts().Insert(context.Background(), draft.Draft{Unit: slackDM, Kind: draft.Reply, Text: "hi",
					Origin: draft.Origin{Kind: draft.OriginSession}, State: draft.Sent, CreatedAt: noteAt, UpdatedAt: noteAt}); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.RehomeDrafts(ctx, map[unit.Unit]unit.Unit{slackDM: slackSeg})
				return err
			},
			topics: port.TopicDrafts, units: []unit.Unit{slackSeg},
		},
	)
}

func TestSourceEventsAreKeptOnceAndFilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	s := New(db)
	merged := port.Event{Unit: u7, Kind: "merged", At: noteAt}
	push := port.Event{Unit: u7, Kind: "push", Key: "abc", Actor: "ana", At: noteAt}
	for range 2 {
		if err := s.RecordSourceEvents(ctx, merged, push); err != nil {
			t.Fatal(err)
		}
	}
	filled := merged
	filled.Actor, filled.Data = "bruno", "def"
	other := filled
	other.Actor = "carla"
	if err := s.RecordSourceEvents(ctx, filled, other); err != nil {
		t.Fatal(err)
	}
	got, err := s.UnitEvents(ctx, []unit.Unit{u7}, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []port.Event{filled, push}
	if !slices.Equal(got, want) {
		t.Errorf("kept %+v, want %+v", got, want)
	}
	if got, err := s.UnitEvents(ctx, []unit.Unit{u7}, []string{"push"}, noteAt.Add(time.Second)); err != nil || len(got) != 0 {
		t.Errorf("pushes since a second later %+v %v, want none", got, err)
	}
}
