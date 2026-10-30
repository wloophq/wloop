package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

func TestEnterWakesTheRunsSessionAsItsIntentLaunchesIt(t *testing.T) {
	t.Parallel()
	te, m := sharedEnv(t, "")
	m.Sessions = nil
	te.gh.basics = openBasics
	te.registerSession(t, dturn.Session{ID: "old", WorkspaceID: sharedWS.ID, Unit: r7.Unit(), AgentSession: "conv-1", StartedAt: testNow.Add(-time.Hour)})
	ctx := context.Background()
	if err := te.repo.EndHandle(ctx, "old", testNow.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := te.repo.Session(ctx, "old")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if err := (runHost{te.Env}).Wake(ctx, r7.Unit(), "address", sess); err != nil {
		t.Fatal(err)
	}
	const name = "triage acme/api#7 09/10 15:00"
	if len(m.Wakes) != 1 || m.Wakes[0].Session != "old" || m.Wakes[0].Title != name || !strings.Contains(m.Wakes[0].Command, " --name '"+name+"'") {
		t.Fatalf("wakes %+v", m.Wakes)
	}
	if !strings.Contains(m.Wakes[0].Command, "--setting-sources user") {
		t.Fatalf("an untrusted pull request's run woke with its checkout's settings: %s", m.Wakes[0].Command)
	}
}
