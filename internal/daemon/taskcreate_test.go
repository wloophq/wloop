package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

func newCreateRig(t *testing.T, edit func(*Deps)) *tasksRig {
	t.Helper()
	r := newTasksRig(t, edit)
	r.s.deps.TaskRecords, r.s.deps.RecordWriter = r.db, r.db
	r.s.deps.Placements = inventory.Placements{Store: inventory.New(r.db)}
	return r
}

func created(t *testing.T, r *tasksRig, body string) api.TaskView {
	t.Helper()
	rec := post(t, r.s, api.TasksPath, body)
	var v api.TaskView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("POST %s: %d %s", body, rec.Code, rec.Body)
	}
	return v
}

func TestCaptureIsTaskCreateWithATitle(t *testing.T) {
	t.Parallel()
	r := newCreateRig(t, nil)
	v := created(t, r, `{"title":"call the bank"}`)
	if v.State != "inbox" || v.Title != "call the bank" || v.Notes != "" || v.Brief != nil || len(v.Props) != 0 {
		t.Fatalf("a capture %+v", v)
	}
	if p := r.page(t, "view=inbox"); len(p.Tasks) != 1 || p.Tasks[0].ID != v.ID {
		t.Fatalf("the Inbox %+v", p.Tasks)
	}
	if _, ok, err := r.s.deps.Placements.Override(context.Background(), task.UnitOf(task.ID(v.ID))); ok || err != nil {
		t.Fatalf("a capture keeps a placement: %v", err)
	}

	full := created(t, r, `{"title":"Add retries","brief":"## Diagnosis\nthe sender gives up\n","props":{"priority":"high","due":"2026-11-02"},"placement":"acme/api"}`)
	if full.Title != "Add retries" || full.Notes != "" {
		t.Fatalf("the task %+v", full)
	}
	var data map[string]string
	if full.Brief == nil || json.Unmarshal(full.Brief.Data, &data) != nil || data["text"] != "## Diagnosis\nthe sender gives up\n" {
		t.Fatalf("the brief %+v", full.Brief)
	}
	if string(full.Props["priority"]) != `"high"` || !strings.HasPrefix(string(full.Props["due"]), `"2026-11-02T`) {
		t.Fatalf("the props %s", full.Props)
	}
	p, ok, err := r.s.deps.Placements.Override(context.Background(), task.UnitOf(task.ID(full.ID)))
	if !ok || err != nil || p.Repo != "acme/api" {
		t.Fatalf("the placement %+v %v %v", p, ok, err)
	}

	for _, bad := range []string{`{"title":"  "}`, `{"title":"x","props":{"priority":"urgent"}}`, `{"title":"x","props":{"nope":"1"}}`,
		`{"title":"x","props":{"effort":"s"}}`, `{"title":"x","placement":"acme/.."}`} {
		if rec := post(t, r.s, api.TasksPath, bad); rec.Code != 400 {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
	if p := r.page(t, "view=inbox"); len(p.Tasks) != 2 {
		t.Fatalf("a refused create made a task: %+v", p.Tasks)
	}
}

func TestTaskCreateFromAnAgentSessionIsWrittenByTheAgent(t *testing.T) {
	t.Parallel()
	r := newCreateRig(t, nil)
	for _, notAgents := range []string{`{"title":"x","props":{"last_nudge":"2026-11-02"}}`, `{"title":"x","props":{"kind":"capture"}}`} {
		if rec := postFrom(t, r.s, "term_1", api.TasksPath, notAgents); rec.Code != 400 || !strings.Contains(rec.Body.String(), "agent") {
			t.Errorf("%s from an agent session: %d %s", notAgents, rec.Code, rec.Body)
		}
	}
	if p := r.page(t, "view=inbox"); len(p.Tasks) != 0 {
		t.Fatalf("a refused create made a task: %+v", p.Tasks)
	}

	for _, yours := range []string{`{"title":"x","props":{"priority":"high"}}`, `{"title":"x","props":{"owner":"slack:U9"}}`, `{"title":"x","props":{"focus":"on"}}`} {
		if rec := postFrom(t, r.s, "term_1", api.TasksPath, yours); rec.Code != 403 || !strings.Contains(rec.Body.String(), "session you drive") {
			t.Errorf("%s from a session the user does not drive: %d %s", yours, rec.Code, rec.Body)
		}
	}
	if p := r.page(t, "view=inbox"); len(p.Tasks) != 0 {
		t.Fatalf("a refused create made a task: %+v", p.Tasks)
	}

	rec := postFrom(t, r.s, "term_1", api.TasksPath, `{"title":"Add retries","brief":"the sender gives up","props":{"effort":"s"}}`)
	var v api.TaskView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("an agent's create: %d %s", rec.Code, rec.Body)
	}
	ctx := context.Background()
	tk, err := r.db.Tasks().Task(ctx, task.ID(v.ID))
	if err != nil {
		t.Fatal(err)
	}
	if by := tk.Written["effort"].By; by != task.WriterAgent {
		t.Errorf("effort written by %q", by)
	}
	u := task.UnitOf(task.ID(v.ID))
	recs, err := r.db.Records(ctx, "brief", u)
	if err != nil || recs[u].Actor != string(task.WriterAgent) {
		t.Fatalf("the brief record %+v %v", recs[u], err)
	}
}

func TestTaskCreateFromASessionTheUserDrivesWritesYourProperties(t *testing.T) {
	t.Parallel()
	r := newCreateRig(t, nil)
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	adoptSession(t, svc, "term_2", true)
	rec := postFrom(t, r.s, "term_2", api.TasksPath, `{"title":"Add retries","props":{"priority":"high","owner":"slack:U9"}}`)
	var v api.TaskView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("a create from a session the user drives: %d %s", rec.Code, rec.Body)
	}
	tk, err := r.db.Tasks().Task(context.Background(), task.ID(v.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"priority", "owner"} {
		if by := tk.Written[name].By; by != task.WriterAgent {
			t.Errorf("%s written by %q", name, by)
		}
	}
}

func TestAHandleNoSessionTookIsAnAgentsAsBefore(t *testing.T) {
	t.Parallel()
	r := newCreateRig(t, nil)
	r.s.deps.Turns = sessionsOn(r.db)
	rec := postFrom(t, r.s, "term_9", api.TasksPath, `{"title":"Add retries","props":{"effort":"s"}}`)
	var v api.TaskView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("a create from a handle no session took: %d %s", rec.Code, rec.Body)
	}
	if rec := postFrom(t, r.s, "term_9", api.TasksPath, `{"title":"x","props":{"priority":"high"}}`); rec.Code != 403 {
		t.Errorf("a handle no session took wrote your property: %d %s", rec.Code, rec.Body)
	}
}

func TestTaskCreateParsesADateInTheDaemonsLocation(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("UTC-3", -3*60*60)
	r := newCreateRig(t, func(d *Deps) { d.Now = func() time.Time { return tasksNow.In(zone) } })
	v := created(t, r, `{"title":"Call Ana","props":{"due":"2026-11-02"}}`)
	tk, err := r.db.Tasks().Task(context.Background(), task.ID(v.ID))
	if err != nil {
		t.Fatal(err)
	}
	if due := tk.Props["due"].Time; !due.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, zone)) {
		t.Fatalf("due %v, want midnight of 2026-11-02 in the daemon's zone", due)
	}
}
