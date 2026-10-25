package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/round"
)

// r7 (acme/api#7) is declared in actions_test.go.

func row7(t *testing.T, s *Server, view string) (api.Row, bool) {
	t.Helper()
	var rows []api.Row
	get(t, s, "/api/rows?view="+view, &rows)
	for _, r := range rows {
		if r.Unit == r7.Unit() {
			return r, true
		}
	}
	return api.Row{}, false
}

func withPending(t *testing.T) *Server {
	t.Helper()
	return newServerWith(t, func(d *Deps) {
		d.Pending = fixedPending{r7.Unit(): round.Pending{Intent: "follow-up", Reason: "ana replied", Since: d.Now(), Deadline: d.Now().Add(time.Minute)}}
	})
}

func TestPendingRoundShowsInNeedsMe(t *testing.T) {
	t.Parallel()
	s := withPending(t)
	row, ok := row7(t, s, api.ViewNeedsMe)
	if !ok || row.Kind != "round_pending" || row.Pending == nil || row.Summary.WaitsOn != "me" {
		t.Fatalf("%+v", row)
	}
	if !strings.Contains(row.Summary.Reason, "follow-up") || !strings.Contains(row.Summary.Reason, "ana replied") {
		t.Fatalf("reason %q", row.Summary.Reason)
	}
}

func TestRunActionWithNothingPendingRunsTheHeldRound(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	actions := &fakeActions{}
	s.deps.Actions = actions
	holdByCap(t, s)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Contains(actions.calls, "run acme/api#7") {
		t.Fatalf("calls %v", actions.calls)
	}
}

func TestGuideWithoutAPlanIsRefused(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/guide", `{"guidance":"x"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "plans no automatic round") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPauseShowsInHealth(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/pause", `{"paused":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if p, err := s.deps.Pauses.Pause(context.Background()); err != nil || !p.On {
		t.Fatal("pause did not stick")
	}
	var h api.Health
	get(t, s, "/api/health", &h)
	if !h.Paused {
		t.Fatal("health does not say paused")
	}
	post(t, s, "/api/pause", `{"paused":false}`)
	get(t, s, "/api/health", &h)
	if h.Paused {
		t.Fatal("still paused")
	}
}

func TestTheAppReportsOnlyRunning(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/app", `{"state":"unavailable"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "state must be running") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var h api.Health
	get(t, s, "/api/health", &h)
	if h.App != nil {
		t.Fatalf("%+v", h.App)
	}
}

func TestDeniedNotificationsAreAHealthWarning(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	post(t, s, "/api/app", `{"state":"running","notifications":"denied"}`)
	var h api.Health
	get(t, s, "/api/health", &h)
	if !slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.Contains(w, "notifications") }) {
		t.Fatalf("warnings %q", h.Warnings)
	}
}

func TestMuteUntilStateSurvivesAPendingRound(t *testing.T) {
	t.Parallel()
	s := withPending(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until_state":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	row7(t, s, api.ViewAll)
	if err := s.expireMutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, err := s.store().Override(context.Background(), r7)
	if err != nil || !o.MuteUntilStateChange {
		t.Fatalf("the pending round cleared the mute: %+v %v", o, err)
	}
}

func TestMutedPullRequestWithAPendingRoundStaysOutOfNeedsMe(t *testing.T) {
	t.Parallel()
	s := withPending(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, ok := row7(t, s, api.ViewNeedsMe); ok {
		t.Fatal("a muted pull request is in needs-me")
	}
	row, ok := row7(t, s, api.ViewAll)
	if !ok || row.Kind != "muted" || row.Pending == nil {
		t.Fatalf("%+v", row)
	}
}

func TestAppStatusOutsideTheKnownValuesIs400(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, body := range []string{`{"state":"sleeping"}`, `{"state":"running","notifications":"maybe"}`} {
		if rec := post(t, s, "/api/app", body); rec.Code != 400 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}
