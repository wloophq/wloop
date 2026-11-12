package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/daemon/agentdraftstest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

type draftSessions struct{}

func (draftSessions) MCPSeen(context.Context, string) error { return nil }
func (draftSessions) RecordTurnEnd(context.Context, turns.RecordRequest) (turns.Recorded, error) {
	return turns.Recorded{}, nil
}
func (draftSessions) SessionOfHandle(_ context.Context, h string) (string, bool, error) {
	return h, true, nil
}

// draftPorts are one fake outbox behind the daemon's agent route over a Unix socket, and another, seeded
// the same, behind the MCP draft tools.
type draftPorts struct {
	cliOutbox, mcpOutbox *agentdraftstest.Outbox
	sock                 string
	mcp                  *mcp.Server
}

func newDraftPorts(t *testing.T) *draftPorts {
	t.Helper()
	pr7 := unit.Unit{Provider: "github", Key: "acme/api#7"}
	agentDrafts := func(ob *agentdraftstest.Outbox) *daemon.AgentDrafts {
		ob.Put(draft.Draft{ID: 42, Unit: pr7, Kind: "review", Delivery: "review", Text: "Two blocking findings.", Payload: json.RawMessage(`{"event":"REQUEST_CHANGES"}`),
			Basis: "4f9c2d1", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginTurn, Session: "term_1"}})
		ob.Put(draft.Draft{ID: 43, Unit: pr7, Kind: "comment", Text: "The user's.", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginUser}})
		return &daemon.AgentDrafts{Outbox: ob, Unit: func(_ context.Context, s string) (unit.Unit, bool, error) { return pr7, s == "term_1", nil }}
	}
	p := &draftPorts{cliOutbox: &agentdraftstest.Outbox{}, mcpOutbox: &agentdraftstest.Outbox{}}
	srv := daemon.New(daemon.Deps{Cfg: Defaults(), Version: "test", AgentDrafts: agentDrafts(p.cliOutbox)})
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p.sock = filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", p.sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(srv.SocketHandler())
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)
	p.mcp = &mcp.Server{Port: 47833, Turns: draftSessions{}, Texts: bundledToolTexts, Bundled: bundledToolTexts, Drafts: agentDrafts(p.mcpOutbox)}
	return p
}

func (p *draftPorts) cli(t *testing.T, args ...string) (string, bool) {
	t.Helper()
	var out, errw bytes.Buffer
	code := draftCommand(context.Background(), &Env{Out: &out, Err: &errw}, client.New(p.sock), "term_1", args)
	if code == 2 {
		t.Fatalf("draft %v: usage: %s", args, errw.String())
	}
	if code != 0 {
		return errw.String(), false
	}
	return out.String(), true
}

func (p *draftPorts) tool(t *testing.T, name, args string) (string, bool) {
	t.Helper()
	post := func(sid, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47833/mcp", strings.NewReader(body))
		req.Header.Set(api.SessionHeader, "term_1")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		p.mcp.ServeHTTP(rec, req)
		return rec
	}
	sid := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`).Header().Get("Mcp-Session-Id")
	rec := post(sid, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	var got struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Result.Content) != 1 {
		t.Fatalf("%s: %v %s", name, err, rec.Body)
	}
	return got.Result.Content[0].Text, !got.Result.IsError
}

// draftIn is the draft a successful answer shows.
func draftIn(t *testing.T, text string) api.AgentDraft {
	t.Helper()
	var d api.AgentDraft
	if err := json.Unmarshal([]byte(text[strings.Index(text, "{"):]), &d); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return d
}

func TestDraftCLIAndMCPSameAnswer(t *testing.T) {
	t.Parallel()
	p := newDraftPorts(t)
	for _, step := range []struct {
		cli         []string
		tool, args  string
		ok          bool
		errContains string
	}{
		{[]string{"read", "42"}, "draft_read", `{"id":42}`, true, ""},
		{[]string{"update", "42", "--rev", "1", "--payload", `{"event":"COMMENT"}`, "--basis", "5a0d3e2"}, "draft_update", `{"id":42,"rev":1,"payload":{"event":"COMMENT"},"basis":"5a0d3e2"}`, true, ""},
		{[]string{"update", "42", "--rev", "1", "--text", "late"}, "draft_update", `{"id":42,"rev":1,"text":"late"}`, false, "changed since it was read"},
		{[]string{"read", "43"}, "draft_read", `{"id":43}`, true, ""},
		{[]string{"update", "43", "--rev", "1", "--text", "mine now"}, "draft_update", `{"id":43,"rev":1,"text":"mine now"}`, false, "out of the caller's reach"},
		{[]string{"withdraw", "42", "--rev", "2", "--reason", "other_option"}, "draft_withdraw", `{"id":42,"rev":2,"reason":"other_option"}`, true, ""},
		{[]string{"read", "99"}, "draft_read", `{"id":99}`, false, "no such draft"},
	} {
		cliOut, cliOK := p.cli(t, step.cli...)
		mcpOut, mcpOK := p.tool(t, step.tool, step.args)
		if cliOK != step.ok || mcpOK != step.ok {
			t.Fatalf("%v: cli ok %v (%s), mcp ok %v (%s), want %v", step.cli, cliOK, cliOut, mcpOK, mcpOut, step.ok)
		}
		if !step.ok {
			if !strings.Contains(cliOut, step.errContains) || !strings.Contains(mcpOut, step.errContains) {
				t.Fatalf("%v: cli %q, mcp %q, want both to say %q", step.cli, cliOut, mcpOut, step.errContains)
			}
			continue
		}
		if strings.TrimSpace(cliOut) != mcp.DraftJSON(draftIn(t, cliOut)) {
			t.Fatalf("%v: the command printed more than the draft: %q", step.cli, cliOut)
		}
		if c, m := draftIn(t, cliOut), draftIn(t, mcpOut); mcp.DraftJSON(c) != mcp.DraftJSON(m) {
			t.Fatalf("%v:\ncli %s\nmcp %s", step.cli, mcp.DraftJSON(c), mcp.DraftJSON(m))
		}
	}
	if c, m := p.cliOutbox.Get(42), p.mcpOutbox.Get(42); c.State != draft.Dropped || c.Reason != "other_option" || c.Rev != m.Rev || c.State != m.State ||
		c.Basis != "5a0d3e2" || string(c.Payload) != string(m.Payload) {
		t.Fatalf("stored: cli %+v, mcp %+v", c, m)
	}
}
