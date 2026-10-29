package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var createdTask = regexp.MustCompile(`created (task:t-[0-9a-f]+)`)

func briefFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// briefText is the markdown task id's own brief record holds.
func briefText(t *testing.T, te *testEnv, id task.ID) string {
	t.Helper()
	recs, err := te.db.Records(context.Background(), builtin.BuildsBrief, task.UnitOf(id))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]string
	if rec, ok := recs[task.UnitOf(id)]; ok {
		if err := json.Unmarshal([]byte(rec.Data), &data); err != nil {
			t.Fatal(err)
		}
	}
	return data[builtin.BriefText]
}

func taskOf(t *testing.T, ref string) task.ID {
	t.Helper()
	return task.ID(strings.TrimPrefix(ref, "task:"))
}

const retriesBrief = "# Add retries to the webhook sender\n\n## Diagnosis\n\nThe sender drops a delivery after one failed attempt.\n\n## Acceptance\n\nA failed delivery is retried three times.\n"

func TestTaskNewSetsTitleBriefAndProps(t *testing.T) {
	te, m, _, _ := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	path := briefFile(t, retriesBrief)
	if code := te.run("task", "new", "--title", "Add retries to the webhook sender", "--brief-file", path,
		"--prop", "priority=high", "--prop", "due=2026-11-02", "--prop", "size=30m", "--repo", "acme/api"); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, te.err, te.out)
	}
	got := createdTask.FindStringSubmatch(te.out.String())
	if got == nil {
		t.Fatalf("no task created:\n%s", te.out)
	}
	id := taskOf(t, got[1])
	tk, err := te.Env.tasks.Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "Add retries to the webhook sender" || tk.Notes != "" || tk.Category != task.CategoryInbox {
		t.Fatalf("the task %+v", tk)
	}
	if v := tk.Props["priority"]; v.Text != "high" || tk.Written["priority"].By != task.WriterPerson {
		t.Fatalf("priority %+v", v)
	}
	if v := tk.Props["due"]; v.Time.IsZero() || v.Time.Format(time.DateOnly) != "2026-11-02" {
		t.Fatalf("due %+v", v)
	}
	if v := tk.Props["size"]; v.Dur != 30*time.Minute {
		t.Fatalf("size %+v", v)
	}
	if b := briefText(t, te, id); b != retriesBrief {
		t.Fatalf("the brief %q", b)
	}
	p, ok, err := inventory.Placements{Store: te.Store}.Override(context.Background(), task.UnitOf(id))
	if err != nil || !ok || p.Repo != "acme/api" {
		t.Fatalf("the placement %+v %v %v", p, ok, err)
	}

	if code := te.run("start", got[1]); code != 0 {
		t.Fatalf("start on the task: exit %d: %s", code, te.err)
	}
	if len(clones) != 1 || !strings.HasPrefix(clones[0], "acme/api ") || len(m.Creates) != 1 || len(m.Prompts) != 1 {
		t.Fatalf("start did not run where the task was created to: clones %q creates %+v", clones, m.Creates)
	}
	round := regexp.MustCompile(`--turn ([0-9a-f]{12})`).FindStringSubmatch(m.Prompts[0].Text)
	if round == nil {
		t.Fatalf("prompt %q", m.Prompts[0].Text)
	}
	if text := storedText(t, te, round[1]); !strings.Contains(text, "The sender drops a delivery after one failed attempt.") {
		t.Fatalf("the instructions lack the brief:\n%s", text)
	}
}

func TestTaskNewRefusesAPropOfTheWrongType(t *testing.T) {
	te, _, _, _ := startEnv(t)
	for _, c := range []struct{ prop, says string }{
		{"priority=urgent", "priority"},
		{"due=next week", "due"},
		{"size=a while", "size"},
		{"prioritty=high", "priority"},
		{"effort=s", "effort"},
	} {
		te.out.Reset()
		te.err.Reset()
		if code := te.run("task", "new", "--title", "Call Ana", "--prop", c.prop); code == 0 || !strings.Contains(te.err.String(), c.says) {
			t.Errorf("--prop %s: exit %d: %s", c.prop, code, te.err)
		}
		if createdTask.MatchString(te.out.String()) {
			t.Errorf("--prop %s created a task: %s", c.prop, te.out)
		}
	}
	all, err := te.Env.tasks.Tasks(context.Background(), task.Query{View: "inbox"})
	if err != nil || len(all) != 0 {
		t.Fatalf("a refused task new left tasks: %+v %v", all, err)
	}
}

func TestStartWithABriefFileKeepsTheTitleShort(t *testing.T) {
	te, m, _, _ := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	if code := te.run("start", "--repo", "acme/api", "--brief-file", briefFile(t, retriesBrief)); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, te.err, te.out)
	}
	got := capturedTask.FindStringSubmatch(te.out.String())
	if got == nil {
		t.Fatalf("no task captured:\n%s", te.out)
	}
	id := taskOf(t, got[1])
	tk, err := te.Env.tasks.Task(context.Background(), id)
	if err != nil || tk.Title != "Add retries to the webhook sender" {
		t.Fatalf("the title %q, %v", tk.Title, err)
	}
	if b := briefText(t, te, id); b != retriesBrief {
		t.Fatalf("the brief %q", b)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	round := regexp.MustCompile(`--turn ([0-9a-f]{12})`).FindStringSubmatch(m.Prompts[0].Text)
	if text := storedText(t, te, round[1]); !strings.Contains(text, "A failed delivery is retried three times.") {
		t.Fatalf("the instructions lack the brief:\n%s", text)
	}

	long := strings.Repeat("retry the webhook delivery with backoff and ", 4) + "log each attempt\n\nmore"
	te.out.Reset()
	if code := te.run("start", "--brief-file", briefFile(t, long)); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	got = capturedTask.FindStringSubmatch(te.out.String())
	tk, err = te.Env.tasks.Task(context.Background(), taskOf(t, got[1]))
	if err != nil || utf8.RuneCountInString(tk.Title) > 73 || !strings.HasPrefix(tk.Title, "retry the webhook delivery with backoff") || strings.Contains(tk.Title, "\n") {
		t.Fatalf("a long first line's title %q, %v", tk.Title, err)
	}

	if code := te.run("start", "--brief-file", briefFile(t, long), "and these words"); code == 0 || !strings.Contains(te.err.String(), "--brief-file") {
		t.Fatalf("a brief file and words: exit %d: %s", code, te.err)
	}
}
