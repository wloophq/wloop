package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
)

type fakeAgent struct {
	calls  int
	handle string
	start  api.SessionStartRequest
	stop   api.StopRequest
	prompt api.PromptRequest
	answer api.StopAnswer
	err    error
	// wait waits on the context and returns its error; boom panics.
	wait, boom bool
}

func (f *fakeAgent) act(ctx context.Context, handle string) error {
	f.calls++
	f.handle = handle
	switch {
	case f.boom:
		panic("boom")
	case f.wait:
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

func (f *fakeAgent) SessionStart(ctx context.Context, handle string, r api.SessionStartRequest) error {
	f.start = r
	return f.act(ctx, handle)
}

func (f *fakeAgent) Stop(ctx context.Context, handle string, r api.StopRequest) (api.StopAnswer, error) {
	f.stop = r
	return f.answer, f.act(ctx, handle)
}

func (f *fakeAgent) Prompt(ctx context.Context, handle string, r api.PromptRequest) error {
	f.prompt = r
	return f.act(ctx, handle)
}

type fakeSession struct{}

func (fakeSession) Handle(getenv func(string) string) string     { return getenv("TERM_HANDLE") }
func (fakeSession) Pane(getenv func(string) string) string       { return getenv("TERM_PANE") }
func (fakeSession) WorktreeID(getenv func(string) string) string { return getenv("TERM_WORKTREE") }

type fakeProtocol struct{}

func (fakeProtocol) SessionID(getenv func(string) string) string { return getenv("AGENT_SESSION") }
func (fakeProtocol) Home(getenv func(string) string) string      { return getenv("AGENT_HOME") }

func (fakeProtocol) SessionStart(raw []byte) (string, string, string, error) {
	var in struct{ ID, From, Dir string }
	err := json.Unmarshal(raw, &in)
	return in.ID, in.From, in.Dir, err
}

func (fakeProtocol) Stop(raw []byte) (string, string, string, error) {
	var in struct{ ID, Prompt, Last string }
	err := json.Unmarshal(raw, &in)
	return in.ID, in.Prompt, in.Last, err
}

func (fakeProtocol) Block(reason string) []byte { return []byte("BLOCK " + reason + "\n") }

type transcriptProtocol struct{ fakeProtocol }

func (transcriptProtocol) Transcript(raw []byte) string {
	var in struct{ Transcript string }
	_ = json.Unmarshal(raw, &in)
	return in.Transcript
}

type promptProtocol struct{ fakeProtocol }

func (promptProtocol) Prompt(raw []byte) (string, error) {
	var in struct{ Prompt string }
	err := json.Unmarshal(raw, &in)
	return in.Prompt, err
}

var ports = harness.Set{
	SessionEnvs:  map[string]harness.SessionEnv{"term": fakeSession{}},
	Agents:       map[string]harness.Protocol{"fake": fakeProtocol{}, "logs": transcriptProtocol{}, "prompts": promptProtocol{}},
	DefaultAgent: "fake",
}

// stdinOf is the hook's standard input; "" is one that never closes.
// bubbled runs f on synctest's clock, which moves only when every goroutine
// waits: a hook that waits out Budget returns at exactly Budget, at once.
func tookFor(name string) time.Duration {
	if name == "input never closes" || name == "client past budget" {
		return Budget
	}
	return 0
}

func bubbled(f func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) { synctest.Test(t, f) }
}

func stdinOf(t *testing.T, s string) io.Reader {
	if s != "" {
		return strings.NewReader(s)
	}
	r, w := io.Pipe()
	t.Cleanup(func() { w.Close() })
	return r
}

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

var inTerm = envOf(map[string]string{"TERM_HANDLE": "term_1", "TERM_PANE": "tab-1:leaf-1", "TERM_WORKTREE": "wt-1", "AGENT_HOME": "/homes/work"})

const stopInput = `{"ID":"sess-1","Prompt":"p1","Last":"all done"}`

func TestStopHookFailsOpen(t *testing.T) {
	for name, c := range map[string]struct {
		env   func(string) string
		in    string
		agent *fakeAgent
		calls int
	}{
		"no handle":          {envOf(nil), stopInput, &fakeAgent{answer: api.StopAnswer{Block: true}}, 0},
		"bad input":          {inTerm, "not json", &fakeAgent{answer: api.StopAnswer{Block: true}}, 0},
		"input never closes": {inTerm, "", &fakeAgent{answer: api.StopAnswer{Block: true}}, 0},
		"client error":       {inTerm, stopInput, &fakeAgent{err: errors.New("daemon: down")}, 1},
		"client past budget": {inTerm, stopInput, &fakeAgent{wait: true, answer: api.StopAnswer{Block: true}}, 1},
		"client panics":      {inTerm, stopInput, &fakeAgent{boom: true}, 1},
	} {
		t.Run(name, bubbled(func(t *testing.T) {
			var out, errw bytes.Buffer
			start := time.Now()
			code := Stop(context.Background(), c.env, stdinOf(t, c.in), &out, &errw, c.agent, ports, "")
			if took, want := time.Since(start), tookFor(name); took != want {
				t.Errorf("took %v, want %v", took, want)
			}
			if code != 0 || out.Len() != 0 || c.agent.calls != c.calls {
				t.Fatalf("exit %d, out %q, calls %d, err %q", code, out.String(), c.agent.calls, errw.String())
			}
			if c.calls > 0 && !strings.Contains(errw.String(), brand.Command+" hook stop: allowing:") {
				t.Fatalf("err %q", errw.String())
			}
		}))
	}
}

func TestHookWithOpenStdinGivesUpWithinBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out, errw bytes.Buffer
		a := &fakeAgent{answer: api.StopAnswer{Block: true, Reason: "r"}}
		start := time.Now()
		code := Stop(context.Background(), inTerm, stdinOf(t, ""), &out, &errw, a, ports, "")
		if took := time.Since(start); took != Budget {
			t.Fatalf("took %v, want Budget", took)
		}
		if code != 0 || out.Len() != 0 || a.calls != 0 || !strings.Contains(errw.String(), brand.Command+" hook stop: allowing:") {
			t.Fatalf("exit %d, out %q, calls %d, err %q", code, out.String(), a.calls, errw.String())
		}
	})
}

func TestHookWithMalformedInputAllows(t *testing.T) {
	for _, in := range []string{"not json", "{", `{"ID":1}`} {
		t.Run(in, bubbled(func(t *testing.T) {
			var out, errw bytes.Buffer
			a := &fakeAgent{answer: api.StopAnswer{Block: true, Reason: "r"}}
			start := time.Now()
			code := Stop(context.Background(), inTerm, strings.NewReader(in), &out, &errw, a, ports, "")
			if took := time.Since(start); took != 0 {
				t.Fatalf("took %v", took)
			}
			if code != 0 || out.Len() != 0 || a.calls != 0 || !strings.Contains(errw.String(), brand.Command+" hook stop: allowing:") {
				t.Fatalf("exit %d, out %q, calls %d, err %q", code, out.String(), a.calls, errw.String())
			}
		}))
	}
}

func TestHookWithAnUnknownKindAllows(t *testing.T) {
	var out, errw bytes.Buffer
	a := &fakeAgent{answer: api.StopAnswer{Block: true, Reason: "r"}}
	if code := Stop(context.Background(), inTerm, strings.NewReader(stopInput), &out, &errw, a, ports, "nope"); code != 0 || out.Len() != 0 || a.calls != 0 {
		t.Fatalf("exit %d, out %q, calls %d", code, out.String(), a.calls)
	}
	if !strings.Contains(errw.String(), brand.Command+" hook stop: allowing:") || !strings.Contains(errw.String(), `"nope"`) {
		t.Fatalf("err %q", errw.String())
	}
	errw.Reset()
	if code := SessionStart(context.Background(), inTerm, strings.NewReader(`{}`), &errw, a, ports, "nope"); code != 0 || a.calls != 0 {
		t.Fatalf("exit %d, calls %d", code, a.calls)
	}
	if !strings.Contains(errw.String(), brand.Command+" hook session-start: going on:") {
		t.Fatalf("err %q", errw.String())
	}
}

func TestHookStopWritesTheProtocolsBlock(t *testing.T) {
	a := &fakeAgent{answer: api.StopAnswer{Block: true, Reason: "record the turn"}}
	var out, errw bytes.Buffer
	if code := Stop(context.Background(), inTerm, strings.NewReader(stopInput), &out, &errw, a, ports, "fake"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := out.String(); got != "BLOCK record the turn\n" {
		t.Fatalf("out %q", got)
	}
	if a.handle != "term_1" || a.stop != (api.StopRequest{AgentSession: "sess-1", PromptID: "p1", LastMessage: "all done", Agent: "fake"}) {
		t.Fatalf("asked %s %+v", a.handle, a.stop)
	}
}

func TestStopSendsTheTranscript(t *testing.T) {
	const in = `{"ID":"sess-1","Prompt":"p1","Last":"done","Transcript":"/t/sess-1.jsonl"}`
	a := &fakeAgent{}
	if code := Stop(context.Background(), inTerm, strings.NewReader(in), io.Discard, io.Discard, a, ports, "logs"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if a.stop.Transcript != "/t/sess-1.jsonl" || a.stop.Agent != "logs" {
		t.Fatalf("stop %+v", a.stop)
	}
	a = &fakeAgent{}
	SessionStart(context.Background(), inTerm, strings.NewReader(in), io.Discard, a, ports, "logs")
	if a.start.Transcript != "/t/sess-1.jsonl" || a.start.Agent != "logs" {
		t.Fatalf("start %+v", a.start)
	}
}

func TestAProtocolWithoutTranscriptsSendsNone(t *testing.T) {
	const in = `{"ID":"sess-1","Transcript":"/t/sess-1.jsonl"}`
	a := &fakeAgent{}
	Stop(context.Background(), inTerm, strings.NewReader(in), io.Discard, io.Discard, a, ports, "fake")
	if a.calls != 1 || a.stop.Transcript != "" {
		t.Fatalf("calls %d, stop %+v", a.calls, a.stop)
	}
	a = &fakeAgent{}
	SessionStart(context.Background(), inTerm, strings.NewReader(in), io.Discard, a, ports, "fake")
	if a.calls != 1 || a.start.Transcript != "" {
		t.Fatalf("calls %d, start %+v", a.calls, a.start)
	}
}

func TestStopHookAllowsSilently(t *testing.T) {
	var out, errw bytes.Buffer
	if code := Stop(context.Background(), inTerm, strings.NewReader(stopInput), &out, &errw, &fakeAgent{}, ports, ""); code != 0 || out.Len() != 0 || errw.Len() != 0 {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errw.String())
	}
}

// stdout captures what f writes on the process's standard output.
func stdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()
	f()
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSessionStartPrintsNothing(t *testing.T) {
	const input = `{"ID":"sess-1","From":"startup","Dir":"/wt/rev"}`
	for name, c := range map[string]struct {
		env   func(string) string
		in    string
		agent *fakeAgent
	}{
		"registered":         {inTerm, input, &fakeAgent{}},
		"no handle":          {envOf(nil), input, &fakeAgent{}},
		"bad input":          {inTerm, "{", &fakeAgent{}},
		"input never closes": {inTerm, "", &fakeAgent{}},
		"client error":       {inTerm, input, &fakeAgent{err: errors.New("daemon: down")}},
		"client past budget": {inTerm, input, &fakeAgent{wait: true}},
		"client panics":      {inTerm, input, &fakeAgent{boom: true}},
	} {
		t.Run(name, bubbled(func(t *testing.T) {
			var errw bytes.Buffer
			code := -1
			start := time.Now()
			if out := stdout(t, func() { code = SessionStart(context.Background(), c.env, stdinOf(t, c.in), &errw, c.agent, ports, "") }); out != "" {
				t.Fatalf("printed %q", out)
			}
			if took, want := time.Since(start), tookFor(name); code != 0 || took != want {
				t.Fatalf("exit %d after %v, want 0 after %v", code, took, want)
			}
		}))
	}
	a := &fakeAgent{}
	SessionStart(context.Background(), inTerm, strings.NewReader(input), io.Discard, a, ports, "")
	want := api.SessionStartRequest{Pane: "tab-1:leaf-1", WorktreeID: "wt-1", AgentSession: "sess-1", Source: "startup", Cwd: "/wt/rev", Home: "/homes/work", Agent: "fake"}
	if a.handle != "term_1" || a.start != want {
		t.Fatalf("sent %s %+v", a.handle, a.start)
	}
}

func TestPromptHookNeverFails(t *testing.T) {
	const input = `{"ID":"sess-1","Prompt":"and the tests?"}`
	for name, c := range map[string]struct {
		env   func(string) string
		in    string
		kind  string
		agent *fakeAgent
		calls int
	}{
		"sent":               {inTerm, input, "prompts", &fakeAgent{}, 1},
		"no handle":          {envOf(nil), input, "prompts", &fakeAgent{}, 0},
		"unknown kind":       {inTerm, input, "nope", &fakeAgent{}, 0},
		"bad input":          {inTerm, "{", "prompts", &fakeAgent{}, 0},
		"input never closes": {inTerm, "", "prompts", &fakeAgent{}, 0},
		"client error":       {inTerm, input, "prompts", &fakeAgent{err: errors.New("daemon: down")}, 1},
		"client past budget": {inTerm, input, "prompts", &fakeAgent{wait: true}, 1},
		"client panics":      {inTerm, input, "prompts", &fakeAgent{boom: true}, 1},
	} {
		t.Run(name, bubbled(func(t *testing.T) {
			var errw bytes.Buffer
			code := -1
			start := time.Now()
			if out := stdout(t, func() { code = Prompt(context.Background(), c.env, stdinOf(t, c.in), &errw, c.agent, ports, c.kind) }); out != "" {
				t.Fatalf("printed %q", out)
			}
			if took, want := time.Since(start), tookFor(name); code != 0 || took != want {
				t.Fatalf("exit %d after %v, want 0 after %v", code, took, want)
			}
			if c.agent.calls != c.calls {
				t.Fatalf("calls %d, err %q", c.agent.calls, errw.String())
			}
			if name != "sent" && name != "no handle" && !strings.Contains(errw.String(), brand.Command+" hook prompt: going on:") {
				t.Fatalf("err %q", errw.String())
			}
		}))
	}
	a := &fakeAgent{}
	Prompt(context.Background(), inTerm, strings.NewReader(input), io.Discard, a, ports, "prompts")
	if a.handle != "term_1" || a.prompt.Prompt != "and the tests?" {
		t.Fatalf("sent %s %+v", a.handle, a.prompt)
	}
}

func TestAProtocolWithoutPromptsSendsAnEmptyOne(t *testing.T) {
	a := &fakeAgent{}
	if code := Prompt(context.Background(), inTerm, strings.NewReader(`{"Prompt":"hi"}`), io.Discard, a, ports, "fake"); code != 0 || a.calls != 1 || a.prompt.Prompt != "" {
		t.Fatalf("exit %d, calls %d, prompt %+v", code, a.calls, a.prompt)
	}
}

func TestMCPHeadersNameTheSessionsHandle(t *testing.T) {
	for want, env := range map[string]func(string) string{`{"X-Loop-Session":"term_1"}`: inTerm, `{}`: envOf(nil)} {
		var out bytes.Buffer
		if code := MCPHeaders(env, &out, ports); code != 0 || strings.TrimSpace(out.String()) != want {
			t.Errorf("exit %d, headers %s, want %s", code, out.String(), want)
		}
	}
}
