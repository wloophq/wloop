package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/daemon/agentdraftstest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func draftCall(tool, args string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
}

func TestDraftToolsOverMCP(t *testing.T) {
	pr7, pr8 := unit.Unit{Provider: "github", Key: "acme/api#7"}, unit.Unit{Provider: "github", Key: "acme/api#8"}
	ob := &agentdraftstest.Outbox{}
	for _, d := range []draft.Draft{
		{ID: 41, Unit: pr7, Kind: "review", Text: "Two findings.", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginTurn, Session: "term_1"}},
		{ID: 42, Unit: pr8, Kind: "review", Text: "Elsewhere.", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginTurn, Session: "term_2"}},
		{ID: 43, Unit: pr7, Kind: "comment", Text: "The user's.", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginUser}},
	} {
		ob.Put(d)
	}
	units := map[string]unit.Unit{"term_1": pr7, "term_2": pr8}
	s := &Server{Texts: bundledTexts, Bundled: bundledTexts, Turns: &fakeTurns{}, Port: 47800,
		Drafts: &daemon.AgentDrafts{Outbox: ob, Unit: func(_ context.Context, session string) (unit.Unit, bool, error) {
			u, ok := units[session]
			return u, ok, nil
		}}}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	names := listedTools(t, call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	for _, n := range []string{"draft_read", "draft_update", "draft_withdraw"} {
		if !slices.Contains(names, n) {
			t.Fatalf("tools/list %v lacks %s", names, n)
		}
	}
	run := func(handle, tool, args string) toolResult {
		t.Helper()
		sid := call(t, s, "", handle, initialize).Header().Get("Mcp-Session-Id")
		return decodeResult(t, call(t, s, sid, handle, draftCall(tool, args)))
	}

	got := run("term_1", "draft_read", `{"id":41}`)
	var read api.AgentDraft
	if err := json.Unmarshal([]byte(got.Result.Content[0].Text), &read); got.Result.IsError || err != nil || read.ID != 41 || read.Rev != 1 || read.Unit != "github/acme/api#7" {
		t.Fatalf("read: %+v %v", got, err)
	}
	for _, tc := range []struct{ handle, tool, args string }{
		{"term_1", "draft_read", `{"id":42}`},
		{"term_1", "draft_update", `{"id":42,"rev":1,"text":"mine now"}`},
		{"term_1", "draft_withdraw", `{"id":43,"rev":1}`},
		{"term_2", "draft_update", `{"id":41,"rev":1,"text":"from another session"}`},
	} {
		if got := run(tc.handle, tc.tool, tc.args); !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "out of the caller's reach") {
			t.Errorf("%s %s %s: %+v, want out of scope", tc.handle, tc.tool, tc.args, got)
		}
	}
	ob.Edit(41, "The person's words.")
	if got := run("term_1", "draft_update", `{"id":41,"rev":1,"text":"stale agent words"}`); !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "changed since it was read") {
		t.Fatalf("rev conflict: %+v", got)
	}
	if d := ob.Get(41); d.Text != "The person's words." {
		t.Fatalf("the person's edit was overwritten: %+v", d)
	}
	if got := run("term_1", "draft_update", `{"id":41,"rev":2,"payload":{"event":"COMMENT"}}`); got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, `"rev": 3`) {
		t.Fatalf("update: %+v", got)
	}
	if got := run("term_1", "draft_withdraw", `{"id":41,"rev":3}`); got.Result.IsError || ob.Get(41).State != draft.Dropped || ob.Get(41).Reason != draft.WithdrawSuperseded {
		t.Fatalf("withdraw: %+v, %+v", got, ob.Get(41))
	}

	before := len(ob.Callers())
	nosid := call(t, s, "", "", initialize).Header().Get("Mcp-Session-Id")
	for _, args := range []string{`{"id":42,"rev":1,"text":"x"}`, `{"id":42,"rev":1,"text":"x","session":"term_2"}`} {
		if got := decodeResult(t, call(t, s, nosid, "", draftCall("draft_update", args))); !got.Result.IsError {
			t.Errorf("no session header, %s: %+v", args, got)
		}
	}
	if got := run("term_1", "draft_update", `{"id":42,"rev":1,"text":"x","session":"term_2"}`); !got.Result.IsError || ob.Get(42).Text != "Elsewhere." {
		t.Errorf("a session named in the arguments: %+v", got)
	}
	if n := len(ob.Callers()); n != before {
		t.Fatalf("calls with no session or a named one reached the outbox: %d", n-before)
	}
}
