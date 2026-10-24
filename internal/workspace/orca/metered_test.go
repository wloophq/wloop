package orca

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestMeteredSamples(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	r := &recorder{canned: canned{"worktree ps": `{"ok":true,"result":{"worktrees":[]}}`, "terminal send": okEmpty}}
	if _, err := (MeteredRPC{Next: r}).Call(ctx, "worktree.ps", map[string]any{"limit": 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := (MeteredRunner{Next: r}).Run(ctx, "terminal", "send", "--terminal", "term_x", "--text", "hi", "--json"); err != nil {
		t.Fatal(err)
	}
	failing := &scriptedRPC{err: &RuntimeError{Kind: KindUnavailable, Method: "status.get", Err: errors.New("dial refused")}}
	_, _ = (MeteredRPC{Next: failing}).Call(ctx, "status.get", nil)
	appErr := &scriptedRPC{raw: `{"ok":false,"error":{"code":"invalid_argument","message":"bad limit"}}`}
	if _, err := (MeteredRPC{Next: appErr}).Call(ctx, "worktree.list", map[string]any{"limit": 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := (MeteredRunner{Next: r}).Run(ctx, "terminal", "send", "extra", "--json"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	failing.err = context.Canceled
	_, _ = (MeteredRPC{Next: failing}).Call(canceled, "status.get", nil)

	ps := metertest.Limit{Res: meter.Workspace, Op: "rpc worktree.ps"}
	send := metertest.Limit{Res: meter.Workspace, Op: "terminal send"}
	status := metertest.Limit{Res: meter.Workspace, Op: "rpc status.get"}
	for l, want := range map[metertest.Limit]int64{
		ps: 1, {Res: ps.Res, Op: ps.Op, Field: metertest.Spawns}: 0,
		{Res: ps.Res, Op: ps.Op, Field: metertest.Bytes}: int64(len(`{"ok":true,"result":{"worktrees":[]}}`)),
		send: 2, {Res: send.Res, Op: send.Op, Field: metertest.Spawns}: 2,
		{Res: send.Res, Op: send.Op, Field: metertest.Bytes}:                     2 * int64(len(okEmpty)),
		{Res: meter.Workspace, Op: "terminal send extra"}:                        0,
		{Res: meter.Workspace, Op: "rpc worktree.list"}:                          1,
		{Res: meter.Workspace, Op: "rpc worktree.list", Field: metertest.Errors}: 0,
		status: 2, {Res: status.Res, Op: status.Op, Field: metertest.Errors}: 1,
	} {
		if got := metertest.Count(sc, l); got != want {
			t.Errorf("%s = %d, want %d\n%s", l, got, want, metertest.Report(sc.Tally()))
		}
	}
	for _, x := range sc.Samples() {
		if x.Op == "terminal send" && x.Detail != "terminal send --terminal term_x --text hi --json" && x.Detail != "terminal send extra --json" {
			t.Errorf("CLI detail = %q", x.Detail)
		}
		if x.Op == "rpc worktree.ps" && x.Detail != `worktree.ps {"limit":1000}` {
			t.Errorf("runtime detail = %q", x.Detail)
		}
	}
}

func TestNewMetersBothPaths(t *testing.T) {
	m := New()
	if r, ok := m.R.(MeteredRunner); !ok || r.Next != (Exec{}) {
		t.Errorf("R = %#v", m.R)
	}
	if rpc, ok := m.RPC.(MeteredRPC); !ok {
		t.Errorf("RPC = %#v", m.RPC)
	} else if _, ok := rpc.Next.(*Runtime); !ok {
		t.Errorf("RPC.Next = %#v", rpc.Next)
	}
}
