package brief

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// bundledProjection is the bundled records.toml's brief projection over
// tasks.toml's risk and effort: an agent writes both, a plugin only risk.
func bundledProjection() Projection {
	values := map[string][]string{"risk": {"low", "medium", "high"}, "effort": {"xs", "s", "m", "l", "xl"}}
	writers := map[string][]task.Writer{"risk": {task.WriterPerson, task.WriterAgent, task.WriterPlugin}, "effort": {task.WriterAgent}}
	return Projection{Projects: map[string]string{"risk": "risk", "size": "effort"},
		Parse: func(prop, text string, w task.Writer) (task.Value, bool) {
			if !slices.Contains(values[prop], text) || !slices.Contains(writers[prop], w) {
				return task.Value{}, false
			}
			return task.Value{Text: text}, true
		}}
}

func TestABriefIsARecordAndProjectsRiskAndEffort(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	id := task.ID("t-0123456789abcdef")
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, Kind: task.KindAction, State: "next",
		Category: task.CategoryReady, Title: "Review the export", Origin: task.OriginUser, Units: []unit.Unit{ref.Unit()}, Created: t0, Updated: t0}}}); err != nil {
		t.Fatal(err)
	}
	r.q.d.Records = &Records{Holders: r.db, Props: r.db.Props(func() time.Time { return t0 }), Projection: bundledProjection}
	r.run.res = &Result{Brief: Brief{Goal: "Export the ledger", Kind: "feature", Size: "m", Risk: "high", OpenQuestions: []string{}, Tags: []string{"lang:go"}}}
	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	recs, err := r.db.Records(ctx, "brief", ref.Unit())
	rec, ok := recs[ref.Unit()]
	if err != nil || !ok || rec.Kind != timeline.KindRecord || rec.Actor != string(task.WriterAgent) {
		t.Fatalf("the brief record %+v %v", recs, err)
	}
	var b Brief
	if err := json.Unmarshal([]byte(rec.Data), &b); err != nil || b.Goal != "Export the ledger" || b.Risk != "high" || !slices.Equal(b.Tags, []string{"lang:go"}) {
		t.Fatalf("the record holds %s: %v", rec.Data, err)
	}
	props, err := r.db.Props(nil).PropsOf(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if props["risk"].Text != "high" || props["effort"].Text != "m" {
		t.Fatalf("the task's properties %+v", props)
	}
	var writer string
	if err := r.db.R().QueryRowContext(ctx, `SELECT writer FROM task_props WHERE task_id = ? AND name = 'risk'`, string(id)).Scan(&writer); err != nil || writer != "agent" {
		t.Fatalf("the risk's writer %q %v", writer, err)
	}
	if _, ok := props["size"]; ok {
		t.Fatal("size is the person's attention, never the brief's effort")
	}
}

func TestBriefTagsComeFromTheRecord(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.run.res = &Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low", OpenQuestions: []string{}, Tags: []string{"area:api", "lang:go"}}}
	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if tags, err := r.st.Tags(ctx, ref); err != nil || !slices.Equal(tags, []string{"area:api", "lang:go"}) {
		t.Fatalf("tags %v %v", tags, err)
	}
	if err := r.db.PutRecord(ctx, ref.Unit(), "brief", task.WriterAgent, []byte(`{"goal":"g","kind":"fix","size":"s","risk":"low","open_questions":[],"tags":["concern:cost"]}`), t0); err != nil {
		t.Fatal(err)
	}
	if tags, _ := r.st.Tags(ctx, ref); !slices.Equal(tags, []string{"concern:cost"}) {
		t.Fatalf("the tags are not the record's: %v", tags)
	}
	sums, _, err := r.q.Briefed(ctx)
	if err != nil || !slices.Equal(sums[ref].Tags, []string{"concern:cost"}) {
		t.Fatalf("the facets' summary %+v %v", sums[ref], err)
	}
	voc, err := r.st.TagVocabulary(ctx, "acme", []string{"concern"}, 5)
	if err != nil || len(voc["concern"]) != 1 || voc["concern"][0].Value != "cost" {
		t.Fatalf("the vocabulary %+v %v", voc, err)
	}
}
