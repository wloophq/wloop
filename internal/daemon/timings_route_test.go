package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestTimingsEndpoint(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if err := s.store().SetMeta(context.Background(), inventory.MetaTimingsSince, "2026-10-01T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	var rep api.TimingsReport
	if code := get(t, s, api.AdminTimings+"?since=7", &rep); code != 200 {
		t.Fatalf("%d", code)
	}
	if !rep.RecordedSince.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !rep.Since.Equal(s.deps.Now().Add(-7*24*time.Hour)) {
		t.Fatalf("report %+v", rep)
	}
	for _, bad := range []string{"0", "x", "400"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", api.AdminTimings+"?since="+bad, nil))
		if rec.Code != 400 {
			t.Errorf("since=%s: %d", bad, rec.Code)
		}
	}
}

func TestARequestOnAPullRequestWithNoTurnTakesTheSweeps(t *testing.T) {
	t.Parallel()
	books := turnstest.NewStore()
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", ID: "by-sweep", Trigger: turn.Automated, State: turn.Sent, SentAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}}})
	s, _ := newIntakeServer(t, nil, func(d *Deps) { d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books} })
	raw, _ := json.Marshal(actionPayload{Action: "review", AfterTurn: 0})
	fresh := intake.Intake{Seq: 2, Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Request: raw}
	if _, ok, err := s.sentWhileWaiting(context.Background(), fresh); err != nil || !ok {
		t.Fatalf("a request on a pull request with no turn missed the sweep's: %v %v", ok, err)
	}
}
