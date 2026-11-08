package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func run(t *testing.T, r *tasksRig, ops ...api.OmniboxOperation) []api.OmniboxResult {
	t.Helper()
	body, _ := json.Marshal(api.OmniboxRun{Operations: ops})
	rec := post(t, r.s, api.OmniboxRunPath, string(body))
	if rec.Code != 200 {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}
	var out api.OmniboxRan
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != len(ops) {
		t.Fatalf("%d results for %d operations", len(out.Results), len(ops))
	}
	return out.Results
}

func TestRunDispatchesThroughHandlers(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	got := run(t, r,
		api.OmniboxOperation{Op: "verb", Task: "t-0000000000000008", Verb: "done"},
		api.OmniboxOperation{Op: "capture", Title: "call Ada"},
		api.OmniboxOperation{Op: "action", Unit: &pastedPR, Action: "track"})
	if !got[0].OK || got[0].Status != 200 {
		t.Fatalf("done: %+v", got[0])
	}
	if e := r.stored(t, "t-0000000000000008"); e.Category != task.CategoryDone {
		t.Fatalf("the task is %s after done", e.Category)
	}
	if !got[1].OK || got[1].Task == "" || r.stored(t, task.ID(got[1].Task)).Title != "call Ada" {
		t.Fatalf("capture: %+v", got[1])
	}
	direct := post(t, r.s, api.UnitPath(pastedPR, "track"), `{"source":"cockpit"}`)
	if got[2].Status != direct.Code {
		t.Fatalf("track answered %d through the omnibox and %d directly", got[2].Status, direct.Code)
	}
}

func TestRunRefusesOperationOutsideVocabulary(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	got := run(t, r,
		api.OmniboxOperation{Op: "verb", Task: "t-0000000000000008", Verb: "approve"},
		api.OmniboxOperation{Op: "action", Unit: &pastedPR, Action: "merge"},
		api.OmniboxOperation{Op: "verb", Task: "t-0000000000000008", Verb: "done"})
	if got[0].OK || got[0].Error != api.OmniboxDropOutsideVocabulary || got[0].Params["name"] != "approve" {
		t.Fatalf("approve: %+v", got[0])
	}
	if got[1].OK || got[1].Params["name"] != "merge" {
		t.Fatalf("merge: %+v", got[1])
	}
	if !got[2].OK {
		t.Fatalf("done, after two refusals: %+v", got[2])
	}
}

func TestRunNeverResolvesAGate(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	g := r.withGates(t, "t-0000000000000008")
	got := run(t, r, api.OmniboxOperation{Op: "verb", Task: "t-0000000000000008", Verb: "approve"},
		api.OmniboxOperation{Op: "verb", Task: "t-0000000000000008", Verb: "answer", Guidance: "yes"})
	if got[0].OK || got[1].OK {
		t.Fatalf("results %+v", got)
	}
	if after, _ := r.s.deps.GateReader.Get(context.Background(), g.ID); after.Status != gate.Open {
		t.Fatalf("the gate is %s", after.Status)
	}
}

func TestRunStartStopsWhenTheCaptureFails(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	before := storedTasks(t, r)
	got := run(t, r, api.OmniboxOperation{Op: "start", Brief: "add retries", Repo: "acme/api"})[0]
	if got.OK || got.Task != "" || got.Error != api.OmniboxRunUnavailable || !strings.Contains(got.Params["detail"], "brief") {
		t.Fatalf("%+v, want the capture's refusal", got)
	}
	if storedTasks(t, r) != before {
		t.Fatal("a refused capture stored a task")
	}
}

func TestRunStartCapturesThenStarts(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	r.s.deps.TaskRecords, r.s.deps.RecordWriter = r.db, r.db
	got := run(t, r, api.OmniboxOperation{Op: "start", Brief: "add retries to the sender\nthree times at most", Repo: "acme/api"})[0]
	if got.Task == "" {
		t.Fatalf("start captured nothing: %+v", got)
	}
	e := r.stored(t, task.ID(got.Task))
	if !strings.HasPrefix(e.Title, "add retries to the sender") {
		t.Fatalf("captured %q", e.Title)
	}
	direct := post(t, r.s, api.UnitPath(task.UnitOf(e.ID), "start"), `{"source":"cockpit","inputs":{"repo":"acme/api"}}`)
	if got.Status != direct.Code {
		t.Fatalf("the start answered %d through the omnibox and %d directly", got.Status, direct.Code)
	}
}

func TestRunKeepsTheHandlersRefusal(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	direct := post(t, r.s, api.TaskPath("t-00000000000000ff", "done"), `{"source":"cockpit"}`)
	got := run(t, r, api.OmniboxOperation{Op: "verb", Task: "t-00000000000000ff", Verb: "done"})[0]
	var answer struct{ Error, Refusal string }
	_ = json.Unmarshal(direct.Body.Bytes(), &answer)
	if got.OK || got.Status != direct.Code || got.Error != api.OmniboxRunNotFound || got.Params["detail"] != answer.Error || got.Refusal != answer.Refusal {
		t.Fatalf("%+v, want the handler's %d %+v", got, direct.Code, answer)
	}
}

func TestRunWordsEachFailureByACode(t *testing.T) {
	for status, want := range map[int]string{400: api.OmniboxRunInvalid, 403: api.OmniboxRunForbidden, 404: api.OmniboxRunNotFound,
		409: api.OmniboxRunConflict, 503: api.OmniboxRunUnavailable, 500: api.OmniboxRunFailed, 502: api.OmniboxRunFailed} {
		if got := runError(status); got != want {
			t.Errorf("%d: %q, want %q", status, got, want)
		}
	}
	strs := bundledLoaded().Strings
	pt := loadedIn(t, "pt-BR").Strings
	for _, code := range []string{api.OmniboxRunInvalid, api.OmniboxRunForbidden, api.OmniboxRunNotFound, api.OmniboxRunConflict, api.OmniboxRunUnavailable, api.OmniboxRunFailed} {
		en, ok := strs[code]
		if !ok || strings.Contains(en["other"], "{") {
			t.Errorf("%s: %v, want English words naming no handler text", code, en)
		}
		if pt[code]["other"] == en["other"] {
			t.Errorf("%s has no pt-BR words", code)
		}
	}
}

func TestRunNeedsAuthLikeTheHandlers(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	unversioned := func(path, body string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Host = "127.0.0.1:47000"
		r.s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	want := unversioned(api.TasksPath, `{"title":"x"}`)
	if got := unversioned(api.OmniboxRunPath, `{"operations":[{"op":"capture","title":"x"}]}`); got != want || got == 200 {
		t.Fatalf("an unversioned run answers %d, a task create %d", got, want)
	}
	if n := storedTasks(t, r); n != 1 {
		t.Fatalf("%d tasks after refused requests", n)
	}
}
