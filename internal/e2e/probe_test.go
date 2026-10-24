//go:build live

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
)

// loopRepo is the repository the probe's workspaces are made in.
var loopRepo = strings.TrimPrefix("github.com/wloophq/wloop", "github.com/")

// TestLiveProbe checks that on every agent home a real session reads its turn,
// gets one Stop block, records through MCP with the header's identity, takes
// an answer typed into it, and is resumed in a new session once closed.
func TestLiveProbe(t *testing.T) {
	l := newLive(t)
	for i, home := range homes(t) {
		t.Run(homeTag(i, home), func(t *testing.T) { probeHome(t, l, i, home) })
	}
}

func probeHome(t *testing.T, l *live, i int, home string) {
	ws := l.createWorkspace(t, loopRepo, l.name(homeTag(i, home)))

	var res api.ProbeResult
	if err := json.Unmarshal(command(t, "probe", "--workspace", ws.ID, "--home", home, "--model", "haiku"), &res); err != nil {
		t.Fatal(err)
	}
	l.own(res.Handle)
	gh, err := github.ParseURL(res.PR)
	if err != nil {
		t.Fatal(err)
	}
	ref := pr.Ref(gh)
	t.Logf("probe %s round %s in %s", res.PR, res.Round, res.Handle)

	var first turn.Turn
	waitFor(t, 3*time.Minute, "the Stop hook blocks the unrecorded turn", func() (bool, string) {
		b := bookOf(t, res.PR)
		for _, e := range b.events(turns.KindStopBlocked) {
			if strings.HasPrefix(e.Key, res.Handle+":") {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%d events, turns %+v", len(b.Events), b.Turns)
	})
	waitFor(t, 3*time.Minute, "the first question is recorded through MCP by the session it was sent to", func() (bool, string) {
		b := bookOf(t, res.PR)
		t0, ok := b.ByRound(res.Round)
		if !ok {
			return false, "no turn of round " + res.Round
		}
		first = *t0
		r, ok := b.record(first.Seq, "probe: first question")
		if !ok {
			return false, fmt.Sprintf("turn %+v, records %+v", first, b.Records)
		}
		if r.Handle != res.Handle {
			t.Fatalf("the record came from %s, the turn was sent to %s", r.Handle, res.Handle)
		}
		for _, e := range b.events(turns.KindTurnEnded) {
			if strings.HasPrefix(e.Key, res.Round+":") && strings.HasSuffix(e.Data, " (via mcp)") {
				return true, ""
			}
		}
		return false, "no turn_ended event (via mcp)"
	})

	l.answer(t, ref, "go", turn.Notification)
	waitFor(t, 3*time.Minute, "the answer is typed into the session, which asks the second question", func() (bool, string) {
		b := bookOf(t, res.PR)
		a, ok := b.answer(first.Seq, "go")
		if !ok || a.Route == turn.Pending || a.Route == turn.Queued {
			return false, fmt.Sprintf("answers %+v", b.Answers)
		}
		if a.Route != turn.Typed || a.Session != res.Handle {
			t.Fatalf("the answer went %s to %q, want typed to %s", a.Route, a.Session, res.Handle)
		}
		r, ok := b.record(first.Seq, "probe: second question")
		if ok && r.Handle != res.Handle {
			t.Fatalf("the second question came from %s", r.Handle)
		}
		return ok, fmt.Sprintf("records %+v", b.Records)
	})

	l.closeTerminal(t, res.Handle)
	waitFor(t, time.Minute, "Orca no longer lists the closed terminal", func() (bool, string) {
		return !l.listed(t, ws.ID, res.Handle), res.Handle + " still listed"
	})

	l.answer(t, ref, "finish", turn.Cockpit)
	var resumed turn.Turn
	waitFor(t, 5*time.Minute, "a new session resumes the probe and records done", func() (bool, string) {
		b := bookOf(t, res.PR)
		a, ok := b.answer(first.Seq, "finish")
		if !ok || a.Route == turn.Pending {
			return false, fmt.Sprintf("answers %+v", b.Answers)
		}
		if a.Route != turn.Resumed {
			t.Fatalf("the answer went %s, want resumed", a.Route)
		}
		for _, x := range b.Turns {
			if x.ResumeOf == first.Seq {
				resumed = x
			}
		}
		if resumed.Seq == 0 || resumed.SentTo == "" {
			return false, fmt.Sprintf("turns %+v", b.Turns)
		}
		if resumed.SentTo == res.Handle {
			t.Fatalf("the resume went to the closed session %s", res.Handle)
		}
		l.own(resumed.SentTo)
		r, ok := b.record(resumed.Seq, "probe: resumed after finish")
		if ok && r.Handle != resumed.SentTo {
			t.Fatalf("the resume's record came from %s, it was sent to %s", r.Handle, resumed.SentTo)
		}
		return ok && r.Record.State == "done", fmt.Sprintf("records %+v", b.Records)
	})

	b := bookOf(t, res.PR)
	if len(b.events(turns.KindTurnResumed)) == 0 {
		t.Error("no turn_resumed event")
	}
	blocks := map[string]int{}
	for _, e := range b.events(turns.KindStopBlocked) {
		blocks[e.Key]++
	}
	for key, n := range blocks {
		if n != 1 {
			t.Errorf("%d stop_blocked events for %s", n, key)
		}
	}
	for _, x := range b.Turns {
		if x.State == turn.Unrecorded {
			t.Errorf("turn %d is unrecorded", x.Seq)
		}
	}
	if got := b.Endorsed(resumed.Seq); got != turn.NotEndorsed {
		t.Errorf("endorsed %q, want %q", got, turn.NotEndorsed)
	}
	if l.listed(t, ws.ID, resumed.SentTo) {
		l.closeTerminal(t, resumed.SentTo)
	}
}
