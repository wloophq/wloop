package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"text/template"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// promptMarks are one classify call's markers around untrusted text, carrying a code no data can guess.
type promptMarks struct{ Nonce, Begin, End string }

func newPromptMarks() promptMarks {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	n := hex.EncodeToString(b)
	return promptMarks{Nonce: n, Begin: "<<<DATA " + n + ">>>", End: "<<<END DATA " + n + ">>>"}
}

func (m promptMarks) wrap(s string) string { return m.Begin + s + m.End }

// promptTask is a task as a with prompt reads it; every text but the id inside the markers.
type promptTask struct {
	ID, Title, Notes string
	Items            []string
}

// task is t's prompt view: its notes cut to withNotesRunes and at most withItems item summaries, open first.
func (m promptMarks) task(id task.ID, title, notes string, summaries []string) promptTask {
	if r := []rune(notes); len(r) > withNotesRunes {
		notes = string(r[:withNotesRunes])
	}
	pt := promptTask{ID: string(id), Title: m.wrap(title), Notes: m.wrap(notes)}
	for _, s := range summaries[:min(len(summaries), withItems)] {
		pt.Items = append(pt.Items, m.wrap(s))
	}
	return pt
}

// withData is what a with prompt's template reads.
type withData struct {
	Facts map[string]any
	Task  *promptTask
	Tasks []promptTask
	promptMarks
}

// markedFacts are f with every untrusted string, and each string of an untrusted list, inside m's markers.
func (m promptMarks) markedFacts(f workflow.Facts) map[string]any {
	out := make(map[string]any, len(f))
	for name, v := range f {
		if !builtin.IsUntrusted(name) && !strings.HasPrefix(name, builtin.PropPrefix) {
			out[name] = v
			continue
		}
		switch x := v.(type) {
		case string:
			out[name] = m.wrap(x)
		case []string:
			l := make([]string, len(x))
			for i, s := range x {
				l[i] = m.wrap(s)
			}
			out[name] = l
		default:
			out[name] = v
		}
	}
	return out
}

// renderWith renders a with prompt's template text over d.
func renderWith(text string, d withData) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("classify has no prompt")
	}
	t, err := template.New("prompt").Option("missingkey=zero").Parse(text)
	if err != nil {
		return "", fmt.Errorf("classify: the prompt: %w", err)
	}
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return "", fmt.Errorf("classify: the prompt: %w", err)
	}
	return b.String(), nil
}

// withPrompt is the prompt of a classify whose with lists ids: text over u's facts, u's task when it is one,
// and the tasks ids name that still exist.
func (c *classifier) withPrompt(ctx context.Context, text string, u unit.Unit, f workflow.Facts, ids []task.ID) (string, error) {
	m := newPromptMarks()
	listed := make([]string, len(ids))
	for i, id := range ids {
		listed[i] = string(id)
	}
	self, tasks, err := c.promptTasks(ctx, m, u, listed)
	if err != nil {
		return "", err
	}
	return renderWith(text, withData{Facts: m.markedFacts(f), Task: self, Tasks: tasks, promptMarks: m})
}

// promptTasks are u's task when it is one, and the tasks of ids that still exist, as the prompt reads them
// inside m's markers.
func (c *classifier) promptTasks(ctx context.Context, m promptMarks, u unit.Unit, ids []string) (*promptTask, []promptTask, error) {
	g, err := c.s.tasks()
	if err != nil {
		return nil, nil, err
	}
	read := c.itemSummaries(ctx)
	var self *promptTask
	if id, ok := task.IDOf(u); ok {
		e, err := g.Task(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		pt, err := c.promptTask(m, e, read)
		if err != nil {
			return nil, nil, err
		}
		self = &pt
	}
	var out []promptTask
	for _, id := range ids {
		e, err := g.Task(ctx, task.ID(id))
		if errors.Is(err, coretasks.ErrNoTask) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		pt, err := c.promptTask(m, e, read)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, pt)
	}
	return self, out, nil
}

func (c *classifier) promptTask(m promptMarks, e task.Task, read func(task.ItemRef) (string, bool, error)) (promptTask, error) {
	var summaries []string
	for _, ref := range append(append([]task.ItemRef(nil), e.Items...), e.Resolved...) {
		if len(summaries) == withItems {
			break
		}
		s, ok, err := read(ref)
		if err != nil {
			return promptTask{}, err
		}
		if ok {
			summaries = append(summaries, s)
		}
	}
	return m.task(e.ID, e.Title, e.Notes, summaries), nil
}

// itemSummaries reads an item's summary, each unit's items once; none without an items ledger.
func (c *classifier) itemSummaries(ctx context.Context) func(task.ItemRef) (string, bool, error) {
	byUnit := map[unit.Unit]map[string]string{}
	return func(ref task.ItemRef) (string, bool, error) {
		if c.s.d.Items == nil {
			return "", false, nil
		}
		got, ok := byUnit[ref.Unit]
		if !ok {
			its, err := c.s.d.Items.Items(ctx, ref.Unit)
			if err != nil {
				return "", false, err
			}
			got = map[string]string{}
			for _, it := range its {
				got[it.ID] = it.Summary
			}
			byUnit[ref.Unit] = got
		}
		s, ok := got[ref.Item]
		return s, ok, nil
	}
}
