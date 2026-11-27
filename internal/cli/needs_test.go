package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// needsCalls are what the CLI asked a needs daemon: each queue read's query, and each post's path and body.
type needsCalls struct {
	sync.Mutex
	queries []url.Values
	posts   []string
	bodies  []api.TaskRequest
}

func (c *needsCalls) postsSeen() ([]string, []api.TaskRequest) {
	c.Lock()
	defer c.Unlock()
	return slices.Clone(c.posts), slices.Clone(c.bodies)
}

// needsDaemon serves q as the queue, whatever the query, and takes every gate resolution and task verb.
func needsDaemon(t *testing.T, te *testEnv, q api.Queue, interactive bool) *needsCalls {
	t.Helper()
	calls := &needsCalls{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(api.Health{}) })
	mux.HandleFunc("GET "+api.QueueAllPath, func(w http.ResponseWriter, r *http.Request) {
		calls.Lock()
		calls.queries = append(calls.queries, r.URL.Query())
		calls.Unlock()
		_ = json.NewEncoder(w).Encode(q)
	})
	record := func(w http.ResponseWriter, r *http.Request, reply any) {
		var req api.TaskRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		calls.Lock()
		calls.posts = append(calls.posts, r.URL.Path)
		calls.bodies = append(calls.bodies, req)
		calls.Unlock()
		_ = json.NewEncoder(w).Encode(reply)
	}
	mux.HandleFunc("POST "+api.GatesPath+"/{id}/{intent}", func(w http.ResponseWriter, r *http.Request) { record(w, r, api.GateView{}) })
	te.Socket = serveSocket(t, mux)
	saved := draftInteractive
	draftInteractive = func(*Env) bool { return interactive }
	t.Cleanup(func() { draftInteractive = saved })
	return calls
}

var needsNow = testNow

func draftJSON(t *testing.T, d api.Draft) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func questionSubject(t *testing.T, m api.Message) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// needsFixture is a queue of four decisions: a question whose options pin drafts, a draft, a plan and a pull
// request's situation.
func needsFixture(t *testing.T) api.Queue {
	pinned := api.Draft{ID: 52, Unit: "github/acme/api#7", Kind: "review", Purpose: "review", State: "held", Rev: 3, Gate: 41,
		Text: "Two findings block the merge.", Label: "acme/api#7", Intents: map[string]string{"approved": "approve", "rejected": "discard"}}
	answer := []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}
	held := api.Draft{ID: 14, Unit: "slack/T1/C1/1700000000.000100", Kind: "reply", Purpose: "reply", State: "held", Rev: 1, Gate: 33,
		Label: "#ops", Title: "When does the export ship?", Text: "Friday, after the freeze.", Intents: map[string]string{"approved": "approve", "rejected": "discard"},
		Origin: api.DraftOrigin{Kind: "user"}, CreatedAt: needsNow.Add(-time.Hour)}
	return api.Queue{View: "needs-you", Title: "Needs you", Empty: "Nothing needs you.", Total: 4, Items: []api.QueueItem{
		{Kind: "answer", Word: "question", Since: needsNow.Add(-2 * time.Hour), Task: api.TaskView{ID: "t-1", Title: "Review Batch export"},
			Gate: &api.GateView{ID: 31, Kind: "question", Status: "open", Intents: answer, Text: "Request changes or approve?",
				Subject: questionSubject(t, api.Message{ID: 9, Text: "Request changes or approve?"})},
			Primary: api.QueueAction{Kind: "gate", ID: "answer", Label: "Answer"},
			Options: []api.QueueOption{
				{ID: "a", Label: "Request changes", Recommended: true, Primary: "Post Request changes",
					Draft: &api.GateView{ID: 41, Kind: "draft", Status: "open", Subject: draftJSON(t, pinned)}},
				{ID: "b", Label: "Wait for the author", Primary: "Post Wait for the author"},
			}},
		{Kind: "send-draft", Word: "draft", Since: needsNow.Add(-time.Hour), Task: api.TaskView{ID: "draft:14", Title: "#ops · When does the export ship?"},
			Gate:    &api.GateView{ID: 33, Kind: "draft", Status: "open", Intents: []api.IntentView{{ID: "approve", Label: "Send"}, {ID: "discard", Label: "Discard"}}, Subject: draftJSON(t, held)},
			Primary: api.QueueAction{Kind: "gate", ID: "approve", Label: "Send"}},
		{Kind: "approve-plan", Word: "plan", Since: needsNow.Add(-30 * time.Minute), Task: api.TaskView{ID: "t-2", Title: "Ship the retries"},
			Gate:    &api.GateView{ID: 35, Kind: "plan", Status: "open", Text: "Run the review workflow?", Intents: []api.IntentView{{ID: "approve", Label: "Run it"}, {ID: "reject", Label: "Skip"}}},
			Primary: api.QueueAction{Kind: "gate", ID: "approve", Label: "Run it"}},
		{Kind: "act", Word: "pull request", Since: needsNow.Add(-10 * time.Minute), Task: api.TaskView{ID: "t-3", Title: "Fix login"},
			Situation: &api.QueueSituation{Provider: "github", Kind: "conflict", Label: "has conflicts", Unit: "github:acme/web#12"},
			Primary:   api.QueueAction{Kind: "open", Label: "Open"}},
	}}
}

func TestNeedsListsTheQueueItemForItem(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	q := needsFixture(t)
	calls := needsDaemon(t, te, q, false)
	if code := te.run("needs", "source:github", "@ops", "--kind", "send-draft", "--kind", "answer"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var refs []string
	for _, l := range strings.Split(strings.TrimSuffix(te.out.String(), "\n"), "\n") {
		fields := strings.Split(l, "\t")
		refs = append(refs, fields[0]+" "+fields[1])
	}
	if want := []string{"31 answer", "33 send-draft", "35 approve-plan", "act@github:acme/web#12 act"}; !slices.Equal(refs, want) {
		t.Fatalf("listed %q, want the queue's items in its order %q", refs, want)
	}
	calls.Lock()
	defer calls.Unlock()
	if len(calls.queries) != 1 || calls.queries[0].Get("q") != "source:github @ops" || !slices.Equal(calls.queries[0]["kind"], []string{"send-draft", "answer"}) {
		t.Fatalf("asked the queue with %v, want the lens query and the kinds passed on", calls.queries)
	}
}

func TestNeedsOnATerminalReadsEachItem(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	needsDaemon(t, te, needsFixture(t), false)
	if code := te.run("needs", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{"Needs you", "31", "question", "Review Batch export", "Request changes or approve?", "33", "Friday, after the freeze.",
		"act@github:acme/web#12", "has conflicts", "needs show <ref>"} {
		if !strings.Contains(out, want) {
			t.Errorf("printed no %q:\n%s", want, out)
		}
	}
	te.out.Reset()
	needsDaemon(t, te, api.Queue{Empty: "Nothing needs you.", Items: []api.QueueItem{}}, false)
	if code := te.run("needs", "--color", "always"); code != 0 || !strings.Contains(te.out.String(), "Nothing needs you.") {
		t.Fatalf("an empty queue: exit %d: %s", code, te.out)
	}
}

func TestNeedsShowSaysWhatAnItemAsksAndTheCommandsItTakes(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	needsDaemon(t, te, needsFixture(t), false)
	for ref, wants := range map[string][]string{
		"31":                     {"Request changes or approve?", "a  Request changes (recommended)", "Two findings block the merge.", "b  Wait for the author", "needs answer 31 a", "needs answer 31 b"},
		"33":                     {"Friday, after the freeze.", "needs approve 33", "draft discard 14"},
		"35":                     {"Run the review workflow?", "needs approve 35"},
		"act@github:acme/web#12": {"has conflicts", "github:acme/web#12", "open '#task=t-3'"},
	} {
		te.out.Reset()
		if code := te.run("needs", "show", ref); code != 0 {
			t.Fatalf("show %s: exit %d: %s", ref, code, te.err)
		}
		for _, want := range wants {
			if !strings.Contains(te.out.String(), want) {
				t.Errorf("show %s printed no %q:\n%s", ref, want, te.out)
			}
		}
	}
	if code := te.run("needs", "show", "99"); code == 0 || !strings.Contains(te.err.String(), "99") {
		t.Fatalf("an item not in the queue: exit %d: %s", code, te.err)
	}
}

func TestNeedsApproveRefusesAStaleDraft(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	q := needsFixture(t)
	var d api.Draft
	if err := json.Unmarshal(q.Items[1].Gate.Subject, &d); err != nil {
		t.Fatal(err)
	}
	d.Stale, d.Notes = true, []string{"Written against an older head of the pull request."}
	q.Items[1].Gate.Subject = draftJSON(t, d)
	calls := needsDaemon(t, te, q, true)
	if code := te.run("needs", "approve", "33", "--yes"); code == 0 || !strings.Contains(te.err.String(), "older head") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if posts, _ := calls.postsSeen(); len(posts) != 0 {
		t.Fatalf("posted %v for a stale draft", posts)
	}
}

func TestNeedsAnswerWithAnOptionResolvesItsDraft(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	calls := needsDaemon(t, te, needsFixture(t), true)
	te.In = strings.NewReader("y\n")
	if code := te.run("needs", "answer", "31", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "Two findings block the merge.") {
		t.Fatalf("answered without showing the draft it posts:\n%s", te.out)
	}
	posts, bodies := calls.postsSeen()
	if !slices.Equal(posts, []string{api.GatesPath + "/31/answer"}) || bodies[0].Args["answer"] != "a" || bodies[0].Args["rev"] != "3" || bodies[0].Source != "terminal" {
		t.Fatalf("posted %v %+v, want the question's answer with the rev of the draft option a pins", posts, bodies)
	}
}

func TestNeedsAnswerWithAnOptionThatPinsNoDraft(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	calls := needsDaemon(t, te, needsFixture(t), false)
	if code := te.run("needs", "answer", "31", "b"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	posts, bodies := calls.postsSeen()
	if !slices.Equal(posts, []string{api.GatesPath + "/31/answer"}) || bodies[0].Args["answer"] != "b" || bodies[0].Args["rev"] != "" {
		t.Fatalf("posted %v %+v", posts, bodies)
	}
	for _, args := range [][]string{{"needs", "answer", "31", "z"}, {"needs", "answer", "31"}, {"needs", "answer", "33", "a"}} {
		te.err.Reset()
		if code := te.run(args...); code == 0 {
			t.Errorf("%v: exit 0", args)
		}
	}
}

func TestNeedsApproveResolvesAPlanWithItsPrimary(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	calls := needsDaemon(t, te, needsFixture(t), false)
	if code := te.run("needs", "approve", "35"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if posts, _ := calls.postsSeen(); !slices.Equal(posts, []string{api.GatesPath + "/35/approve"}) {
		t.Fatalf("posted %v", posts)
	}
	if code := te.run("needs", "approve", "act@github:acme/web#12"); code == 0 || !strings.Contains(te.err.String(), "no gate") {
		t.Fatalf("a situation: exit %d: %s", code, te.err)
	}
}

func TestNeedsOpenOpensTheCockpitOnTheSameQueue(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	needsDaemon(t, te, needsFixture(t), false)
	if err := os.WriteFile(filepath.Join(te.StateDir, "http.json"), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var opened []string
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		opened = append(opened, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if code := te.run("needs", "source:github", "--kind", "answer", "--open"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := browserCommand() + " http://127.0.0.1:4321/?token=tok#view=needs-you&kind=answer&q=source%3Agithub"; !slices.Equal(opened, []string{want}) {
		t.Fatalf("opened %v, want %s", opened, want)
	}
	if code := te.run("needs", "--kind", "answer", "--kind", "act", "--open"); code != 2 {
		t.Fatalf("two kinds the cockpit cannot narrow by: exit %d: %s", code, te.err)
	}
}

func TestOutboxIsNoCommand(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("outbox"); code != 2 || !strings.HasPrefix(te.err.String(), "unknown command 'outbox'") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}
