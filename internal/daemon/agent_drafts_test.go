package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/daemon/agentdraftstest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	draftsPR7 = unit.Unit{Provider: "github", Key: "acme/api#7"}
	draftsPR8 = unit.Unit{Provider: "github", Key: "acme/api#8"}
)

const draftsSession = "s-octocat-1"

// agentDraftsDaemon serves a daemon over a Unix socket whose agent drafts are ob's, each session's turns on
// units.
func agentDraftsDaemon(t testing.TB, ob *agentdraftstest.Outbox, units map[string]unit.Unit) (*Server, string) {
	t.Helper()
	a := &AgentDrafts{Outbox: ob, Unit: func(_ context.Context, session string) (unit.Unit, bool, error) {
		u, ok := units[session]
		return u, ok, nil
	}}
	s := New(Deps{Cfg: config.Defaults(), Version: "test", AgentDrafts: a})
	stopOnCleanup(t, s)
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(s.SocketHandler())
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)
	return s, sock
}

func heldDraft(id draft.ID, u unit.Unit, origin draft.OriginKind) draft.Draft {
	return draft.Draft{ID: id, Unit: u, Kind: "review", Delivery: "review", Text: "Two blocking findings.", Payload: json.RawMessage(`{"event":"REQUEST_CHANGES"}`),
		Basis: "4f9c2d1", State: draft.Held, Origin: draft.Origin{Kind: origin, Session: draftsSession}}
}

func statusOf(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func TestAgentDraftOutOfScope(t *testing.T) {
	t.Parallel()
	ob := &agentdraftstest.Outbox{}
	ob.Put(heldDraft(41, draftsPR7, draft.OriginTurn))
	ob.Put(heldDraft(42, draftsPR8, draft.OriginTurn))
	ob.Put(heldDraft(43, draftsPR7, draft.OriginUser))
	ob.Put(heldDraft(44, draftsPR7, draft.OriginAuto))
	_, sock := agentDraftsDaemon(t, ob, map[string]unit.Unit{draftsSession: draftsPR7, "s-hubot-2": draftsPR8})
	c := client.New(sock)
	ctx := context.Background()
	text := "Moved the finding to the body."
	for _, id := range []int64{42, 43, 44} {
		if _, err := c.ReadDraft(ctx, draftsSession, id); !strings.Contains(statusOf(err), outbox.ErrOutOfScope.Error()) {
			t.Errorf("read %d: %v, want out of scope", id, err)
		}
		if _, err := c.UpdateDraft(ctx, draftsSession, api.AgentDraftUpdate{ID: id, Rev: 1, Text: &text}); !strings.Contains(statusOf(err), outbox.ErrOutOfScope.Error()) {
			t.Errorf("update %d: %v, want out of scope", id, err)
		}
		if _, err := c.WithdrawDraft(ctx, draftsSession, api.AgentDraftWithdrawal{ID: id, Rev: 1}); !strings.Contains(statusOf(err), outbox.ErrOutOfScope.Error()) {
			t.Errorf("withdraw %d: %v, want out of scope", id, err)
		}
		if d := ob.Get(draft.ID(id)); d.Rev != 1 || d.State != draft.Held || d.Text != "Two blocking findings." {
			t.Errorf("draft %d changed: %+v", id, d)
		}
	}
	if _, err := c.UpdateDraft(ctx, "s-hubot-2", api.AgentDraftUpdate{ID: 41, Rev: 1, Text: &text}); !strings.Contains(statusOf(err), outbox.ErrOutOfScope.Error()) {
		t.Errorf("another session's unit: %v, want out of scope", err)
	}
	if _, err := c.UpdateDraft(ctx, "s-nobody-3", api.AgentDraftUpdate{ID: 41, Rev: 1, Text: &text}); !strings.Contains(statusOf(err), outbox.ErrOutOfScope.Error()) {
		t.Errorf("a session with no turn: %v, want out of scope", err)
	}
	got, err := c.UpdateDraft(ctx, draftsSession, api.AgentDraftUpdate{ID: 41, Rev: 1, Text: &text})
	if err != nil || got.Rev != 2 || got.Text != text || len(got.Revisions) != 2 || got.Revisions[1].By != "session" || got.Revisions[1].Session != draftsSession {
		t.Fatalf("own draft: %+v, %v", got, err)
	}
	for _, c := range ob.Callers() {
		want := map[string][]unit.Unit{draftsSession: {draftsPR7}, "s-hubot-2": {draftsPR8}, "s-nobody-3": nil}[c.Session]
		if !slices.Equal(c.Units, want) {
			t.Errorf("caller %+v, want units %v", c, want)
		}
	}
}

func TestAgentDraftRevConflict(t *testing.T) {
	t.Parallel()
	ob := &agentdraftstest.Outbox{}
	ob.Put(heldDraft(42, draftsPR7, draft.OriginTurn))
	_, sock := agentDraftsDaemon(t, ob, map[string]unit.Unit{draftsSession: draftsPR7})
	c := client.New(sock)
	ctx := context.Background()
	ob.Edit(42, "The person's words.")
	agent := "The agent's words."
	if _, err := c.UpdateDraft(ctx, draftsSession, api.AgentDraftUpdate{ID: 42, Rev: 1, Text: &agent}); !strings.Contains(statusOf(err), outbox.ErrRevConflict.Error()) {
		t.Fatalf("stale rev: %v, want a rev conflict", err)
	}
	if _, err := c.WithdrawDraft(ctx, draftsSession, api.AgentDraftWithdrawal{ID: 42, Rev: 1}); !strings.Contains(statusOf(err), outbox.ErrRevConflict.Error()) {
		t.Fatalf("stale withdraw: %v, want a rev conflict", err)
	}
	if d := ob.Get(42); d.Text != "The person's words." || d.Rev != 2 || d.State != draft.Held {
		t.Fatalf("the person's edit was overwritten: %+v", d)
	}
	read, err := c.ReadDraft(ctx, draftsSession, 42)
	if err != nil || read.Rev != 2 || read.Text != "The person's words." || len(read.Revisions) != 2 || read.Revisions[1].By != "person" {
		t.Fatalf("read: %+v, %v", read, err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			text := "Agent revision " + string(rune('A'+i))
			_, errs[i] = c.UpdateDraft(ctx, draftsSession, api.AgentDraftUpdate{ID: 42, Rev: read.Rev, Text: &text})
		})
	}
	wg.Wait()
	var ok, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case strings.Contains(err.Error(), outbox.ErrRevConflict.Error()):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 || ob.Get(42).Rev != 3 {
		t.Fatalf("two revisions at rev 2: %d applied, %d conflicts, rev %d", ok, conflicts, ob.Get(42).Rev)
	}
}

func TestAgentDraftNoSessionRefused(t *testing.T) {
	t.Parallel()
	ob := &agentdraftstest.Outbox{}
	ob.Put(heldDraft(42, draftsPR7, draft.OriginTurn))
	s, sock := agentDraftsDaemon(t, ob, map[string]unit.Unit{draftsSession: draftsPR7})
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}, Timeout: 10 * time.Second}
	do := func(method, path, header, body string) int {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+brand.Name+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(api.Header, api.Version)
		if header != "" {
			req.Header.Set(api.SessionHeader, header)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, tc := range []struct{ method, path, header, body string }{
		{http.MethodGet, api.AgentDrafts + "?id=42", "", ""},
		{http.MethodPatch, api.AgentDrafts, "", `{"id":42,"rev":1,"text":"x","session":"` + draftsSession + `"}`},
		{http.MethodDelete, api.AgentDrafts, "", `{"id":42,"rev":1,"session":"` + draftsSession + `"}`},
		{http.MethodPost, api.AgentDrafts, "", `{"kind":"review","text":"x","session":"` + draftsSession + `"}`},
		{http.MethodPatch, api.AgentDrafts, "s-hubot-2", `{"id":42,"rev":1,"text":"x","session":"` + draftsSession + `"}`},
		{http.MethodPatch, api.AgentDrafts, "${ORCA_TERMINAL}", `{"id":42,"rev":1,"text":"x"}`},
	} {
		if got := do(tc.method, tc.path, tc.header, tc.body); got != http.StatusBadRequest {
			t.Errorf("%s with header %q and %s: %d, want 400", tc.method, tc.header, tc.body, got)
		}
	}
	if calls := ob.Callers(); len(calls) != 0 {
		t.Fatalf("the outbox was called: %+v", calls)
	}
	if d := ob.Get(42); d.Rev != 1 || d.Text != "Two blocking findings." {
		t.Fatalf("draft changed: %+v", d)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+api.AgentDrafts+"?id=42", nil)
	req.Header.Set(api.SessionHeader, draftsSession)
	req.Header.Set(api.Header, api.Version)
	s.AuthHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the TCP port served the agent route: %d", rec.Code)
	}
	if _, err := (&AgentDrafts{Outbox: ob}).Read(context.Background(), "", 42); !errors.Is(err, ErrNoAgentSession) {
		t.Fatalf("no session in process: %v", err)
	}
}

func TestAgentDraftProposeOnTheSessionsUnit(t *testing.T) {
	t.Parallel()
	ob := &agentdraftstest.Outbox{}
	var proposed []unit.Unit
	a := &AgentDrafts{Outbox: ob, Unit: func(_ context.Context, session string) (unit.Unit, bool, error) { return draftsPR7, session == draftsSession, nil },
		Proposer: func(_ context.Context, session string, u unit.Unit, p api.AgentDraftProposal) (draft.ID, error) {
			proposed = append(proposed, u)
			id := draft.ID(100 + len(proposed))
			ob.Put(draft.Draft{ID: id, Unit: u, Kind: draft.Kind(p.Kind), Text: p.Text, Payload: p.Payload, Basis: p.Basis, Hold: p.Hold, State: draft.Held,
				Origin: draft.Origin{Kind: draft.OriginTurn, Session: session}})
			return id, nil
		}}
	ctx := context.Background()
	got, err := a.Propose(ctx, draftsSession, api.AgentDraftProposal{Kind: "review", Text: "Looks fine.", Payload: json.RawMessage(`{"event":"COMMENT"}`), Basis: "4f9c2d1", Hold: true})
	if err != nil || got.ID != 101 || got.Unit != "github/acme/api#7" || !got.Hold || got.Basis != "4f9c2d1" {
		t.Fatalf("propose: %+v, %v", got, err)
	}
	if got, err := a.Propose(ctx, draftsSession, api.AgentDraftProposal{Unit: "github/acme/api#8", Kind: "comment", Text: "x"}); err != nil || proposed[1] != draftsPR8 || got.ID != 102 {
		t.Fatalf("on another unit the host allows: %+v, %v, proposed %v", got, err, proposed)
	}
	if _, err := a.Read(ctx, draftsSession, 102); !errors.Is(err, outbox.ErrOutOfScope) {
		t.Fatalf("a later read of a draft off the session's unit: %v", err)
	}
	if _, err := a.Propose(ctx, "s-nobody-3", api.AgentDraftProposal{Kind: "comment", Text: "x"}); !errors.Is(err, ErrNoDraftUnit) {
		t.Fatalf("no turn, no unit: %v", err)
	}
	if _, err := a.Propose(ctx, draftsSession, api.AgentDraftProposal{Unit: "acme-api-7", Kind: "comment", Text: "x"}); err == nil || len(proposed) != 2 {
		t.Fatalf("a unit that is no <provider>/<key>: %v", err)
	}
}
