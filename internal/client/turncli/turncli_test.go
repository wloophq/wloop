package turncli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
)

type fakeAgent struct {
	ends    []api.TurnEndRequest
	handles []string
	ended   api.TurnEnded
	answer  api.TurnText
	err     error
	args    []string
	// messages are the message ids TurnAnswer was asked for.
	messages []int64
}

func (f *fakeAgent) EndTurn(_ context.Context, handle string, r api.TurnEndRequest) (api.TurnEnded, error) {
	f.ends, f.handles = append(f.ends, r), append(f.handles, handle)
	return f.ended, f.err
}

func (f *fakeAgent) TurnAnswer(_ context.Context, handle, arg string, message int64) (api.TurnText, error) {
	f.handles, f.args, f.messages = append(f.handles, handle), append(f.args, arg), append(f.messages, message)
	return f.answer, f.err
}

var inTerm = func(k string) string {
	return map[string]string{"TERM_HANDLE": "term_1", "AGENT_SESSION": "sess-1"}[k]
}

type fakeSession struct{}

func (fakeSession) Handle(getenv func(string) string) string { return getenv("TERM_HANDLE") }
func (fakeSession) Pane(func(string) string) string          { return "" }
func (fakeSession) WorktreeID(func(string) string) string    { return "" }

type fakeProtocol struct{ name string }

func (p fakeProtocol) SessionID(getenv func(string) string) string {
	return getenv(p.name + "_SESSION")
}
func (fakeProtocol) Home(func(string) string) string { return "" }
func (fakeProtocol) SessionStart([]byte) (string, string, string, error) {
	return "", "", "", nil
}
func (fakeProtocol) Stop([]byte) (string, string, string, error) { return "", "", "", nil }
func (fakeProtocol) Block(string) []byte                         { return nil }

var ports = harness.Set{
	SessionEnvs:  map[string]harness.SessionEnv{"term": fakeSession{}},
	Agents:       map[string]harness.Protocol{"agent": fakeProtocol{"AGENT"}, "other": fakeProtocol{"OTHER"}},
	DefaultAgent: "other",
}

const url = "https://github.com/acme/api/pull/7"

func recordFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "turn-abc123abc123.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTurnEndRefusesAMalformedRecordAndStoresNothing(t *testing.T) {
	for name, body := range map[string]string{
		"bad fields":    `{"state":"finished","summary":"# heading"}`,
		"unknown field": `{"state":"done","summary":"ok","verdict":"APPROVE"}`,
		"not json":      `state: done`,
	} {
		t.Run(name, func(t *testing.T) {
			path := recordFile(t, body)
			a := &fakeAgent{}
			var out, errw bytes.Buffer
			if code := End(context.Background(), inTerm, []string{url, "--turn", "abc123abc123", "--file", path}, &out, &errw, a, ports); code != 1 {
				t.Fatalf("exit %d: %s", code, errw.String())
			}
			if len(a.ends) != 0 || out.Len() != 0 {
				t.Fatalf("sent %+v, out %q", a.ends, out.String())
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the file is gone: %v", err)
			}
			if !strings.Contains(errw.String(), "fix every problem") {
				t.Fatalf("err %q", errw.String())
			}
		})
	}
	path := recordFile(t, `{"state":"finished","summary":"# heading"}`)
	var errw bytes.Buffer
	End(context.Background(), inTerm, []string{url, "--turn", "abc123abc123", "--file", path}, &bytes.Buffer{}, &errw, &fakeAgent{}, ports)
	if !strings.Contains(errw.String(), "state:") || !strings.Contains(errw.String(), "summary:") {
		t.Fatalf("not every problem: %q", errw.String())
	}
}

func TestTurnEndFailsLoudlyWithoutTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := recordFile(t, `{"state":"failed","summary":"the build broke","resume":"rerun make"}`)
	var out, errw bytes.Buffer
	start := time.Now()
	code := End(context.Background(), inTerm, []string{url, "--turn", "abc123abc123", "--file", path}, &out, &errw, client.New(filepath.Join(dir, "daemon.sock")), ports)
	if took := time.Since(start); code != 1 || took > time.Second {
		t.Fatalf("exit %d after %v: %s", code, took, errw.String())
	}
	if msg := errw.String(); !strings.Contains(msg, brand.Name+"'s daemon is not running, so this record was not stored") || !strings.Contains(msg, "launchctl kickstart gui/") {
		t.Fatalf("err %q", msg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file is gone: %v", err)
	}
}

func TestTurnEndStoresAndDeletesTheFile(t *testing.T) {
	path := recordFile(t, `{"state":"done","summary":"triaged"}`)
	a := &fakeAgent{ended: api.TurnEnded{PR: url, State: "done", Added: true}}
	var out, errw bytes.Buffer
	if code := End(context.Background(), inTerm, []string{url, "--turn", "abc123abc123", "--file", path}, &out, &errw, a, ports); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if len(a.ends) != 1 || a.handles[0] != "term_1" {
		t.Fatalf("sent %+v from %v", a.ends, a.handles)
	}
	got := a.ends[0]
	if got.Turn != "abc123abc123" || got.PR != url || got.AgentSession != "sess-1" || got.WrittenAt.IsZero() || !strings.Contains(string(got.Record), `"triaged"`) {
		t.Fatalf("sent %+v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file is still there: %v", err)
	}
	if out.String() != "Recorded done for acme/api#7. End your turn.\n" {
		t.Fatalf("out %q", out.String())
	}
}

func TestTurnEndShowsALateNote(t *testing.T) {
	path := recordFile(t, `{"state":"done","summary":"triaged"}`)
	a := &fakeAgent{ended: api.TurnEnded{PR: url, State: "done", Added: true, Note: "this turn was replaced by round def; your record changes nothing"}}
	var out bytes.Buffer
	if code := End(context.Background(), inTerm, []string{url, "--turn", "abc123abc123", "--file", path}, &out, &bytes.Buffer{}, a, ports); code != 0 || !strings.Contains(out.String(), "but this turn was replaced by round def") {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

func TestTurnEndKeepsTheFileWhenTheDaemonRefuses(t *testing.T) {
	path := recordFile(t, `{"state":"done","summary":"triaged"}`)
	a := &fakeAgent{err: errors.New("daemon: this turn belongs to another session; record only the turn your instructions name")}
	var errw bytes.Buffer
	if code := End(context.Background(), inTerm, []string{"--turn", "abc123abc123", "--file", path, url}, &bytes.Buffer{}, &errw, a, ports); code != 1 || !strings.Contains(errw.String(), "belongs to another session") {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file is gone: %v", err)
	}
}

func TestTurnEndNeedsItsArguments(t *testing.T) {
	for _, args := range [][]string{{url}, {url, "--turn", "x"}, {"--turn", "x", "--file", "f"}, {url, url, "--turn", "x", "--file", "f"}} {
		var errw bytes.Buffer
		if code := End(context.Background(), inTerm, args, &bytes.Buffer{}, &errw, &fakeAgent{}, ports); code != 2 || !strings.Contains(errw.String(), endUsage) {
			t.Errorf("%v: exit %d: %s", args, code, errw.String())
		}
	}
}

func TestTurnAnswerReadsOneMessage(t *testing.T) {
	a := &fakeAgent{answer: api.TurnText{Text: "Message 12: Which column?"}}
	var out, errw bytes.Buffer
	if code := Answer(context.Background(), inTerm, []string{url, "--message", "12"}, &out, &errw, a, ports); code != 0 || a.messages[0] != 12 || a.args[0] != url {
		t.Fatalf("exit %d: %s; asked %v %v", code, errw.String(), a.args, a.messages)
	}
	for _, bad := range [][]string{{url, "--message"}, {url, "--message", "x"}, {url, "--message", "-1"}, {url, url}, {url, "--other", "1"}} {
		if code := Answer(context.Background(), inTerm, bad, &out, &errw, a, ports); code != 2 {
			t.Errorf("%q: exit %d", bad, code)
		}
	}
}

func TestTurnAnswerPrintsTheNewestAnswer(t *testing.T) {
	a := &fakeAgent{answer: api.TurnText{Text: "first\nsecond"}}
	var out bytes.Buffer
	if code := Answer(context.Background(), inTerm, []string{url}, &out, &bytes.Buffer{}, a, ports); code != 0 || out.String() != "first\nsecond\n" || a.handles[0] != "term_1" {
		t.Fatalf("exit %d out %q handles %v", code, out.String(), a.handles)
	}
	a = &fakeAgent{err: errors.New("daemon: no answer stored for acme/api#7")}
	var errw bytes.Buffer
	if code := Answer(context.Background(), inTerm, []string{url}, &out, &errw, a, ports); code != 1 || !strings.Contains(errw.String(), "no answer stored for acme/api#7") {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
}

func TestTurnCommandsTakeAUnitArgument(t *testing.T) {
	path := recordFile(t, `{"state":"done","summary":"answered"}`)
	a := &fakeAgent{ended: api.TurnEnded{State: "done"}, answer: api.TurnText{Text: "yes"}}
	var out, errw bytes.Buffer
	if code := End(context.Background(), inTerm, []string{"thread:1700000000.1", "--turn", "abc123abc123", "--file", path}, &out, &errw, a, ports); code != 0 {
		t.Fatalf("end: exit %d: %s", code, errw.String())
	}
	if len(a.ends) != 1 || a.ends[0].PR != "thread:1700000000.1" || out.String() != "Recorded done for thread:1700000000.1. End your turn.\n" {
		t.Fatalf("ends %+v, out %q", a.ends, out.String())
	}
	out.Reset()
	if code := Answer(context.Background(), inTerm, []string{"thread:1700000000.1"}, &out, &errw, a, ports); code != 0 || out.String() != "yes\n" {
		t.Fatalf("answer: exit %d: %q %s", code, out.String(), errw.String())
	}
	if code := Answer(context.Background(), inTerm, []string{url}, &out, &errw, a, ports); code != 0 || a.args[1] != url {
		t.Fatalf("answer by url: exit %d, args %q", code, a.args)
	}
	for _, bad := range []string{"https://example.com/x", "not a unit", "Thread:x"} {
		errw.Reset()
		if code := Answer(context.Background(), inTerm, []string{bad}, &out, &errw, a, ports); code != 1 {
			t.Errorf("%q: exit %d", bad, code)
		}
	}
}
