package readmodel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/fakethread"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// summaries is a provider no core package knows: each unit's row comes from
// its generic summary alone.
type summaries struct {
	mu   sync.Mutex
	sums map[unit.Unit]readmodel.Summary
}

func (p *summaries) set(u unit.Unit, s readmodel.Summary) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sums[u] = s
}

func (p *summaries) drop(u unit.Unit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sums, u)
}

func (p *summaries) Provider() unit.ProviderID { return "thread" }

func (p *summaries) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.SortedFunc(maps.Keys(p.sums), api.CompareUnits), nil
}

func (p *summaries) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[unit.Unit]any{}
	for _, u := range units {
		out[u] = p.sums[u]
	}
	return out, nil
}

func (p *summaries) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	s := in.(readmodel.Summary)
	s.Reason = fmt.Sprintf("%d open items", s.OpenItems)
	r := api.Row{Link: "https://chat.example/" + s.WaitsOn, Label: "#general", Summary: s, Views: []string{}}
	if s.WaitsOn == "me" {
		r.Views = append(r.Views, api.ViewNeedsMe)
	}
	return r, nil
}

func TestReadModelServesAFakeProvider(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	a, b := unit.Unit{Provider: "thread", Key: "C1/171.1"}, unit.Unit{Provider: "thread", Key: "C1/171.2"}
	p := &summaries{sums: map[unit.Unit]readmodel.Summary{
		a: {WaitsOn: "me", Since: at.Add(-time.Hour), OpenItems: 2},
		b: {WaitsOn: "ana", Since: at.Add(-72 * time.Hour), OpenItems: 0, Stalled: true},
	}}
	log := change.NewLog(change.DefaultWindow, nil)
	m := readmodel.New(log, nil, nil, nil, nil, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := func(view string, since api.Since) api.RowsPage {
		t.Helper()
		page, err := m.Rows(ctx, view, since)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	full := rows(api.ViewAll, api.Since{})
	if !full.Full || len(full.Rows) != 2 || full.Rows[0].Unit != b || full.Rows[1].Unit != a {
		t.Fatalf("full page: %+v", full)
	}
	if r := full.Rows[0]; !r.Summary.Stalled || r.Summary.WaitsOn != "ana" || r.Summary.Reason != "0 open items" || r.Label != "#general" || r.Link != "https://chat.example/ana" {
		t.Fatalf("the row of %s does not say its summary: %+v", b, r)
	}
	if needs := rows(api.ViewNeedsMe, api.Since{}); len(needs.Rows) != 1 || needs.Rows[0].Unit != a {
		t.Fatalf("needs-me: %+v", needs.Rows)
	}

	published := make(chan api.Change, 4)
	done := make(chan struct{})
	running := runStarted(m)
	go func() {
		m.Run(ctx, func(c api.Change) { published <- c })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	<-running
	next := func(what string) api.Change {
		t.Helper()
		select {
		case c := <-published:
			return c
		case <-time.After(3 * time.Second):
			t.Fatalf("%s published nothing", what)
		}
		return api.Change{}
	}

	needsAt := api.Since{Epoch: full.Epoch, Rev: full.Rev}
	p.set(b, readmodel.Summary{WaitsOn: "me", Since: at, OpenItems: 1})
	log.Note(port.TopicPRs, b)
	if c := next("a summary change"); c.All || !slices.Equal(c.Units, []unit.Unit{b}) {
		t.Fatalf("change %+v", c)
	}
	delta := rows(api.ViewAll, needsAt)
	if delta.Full || len(delta.Rows) != 1 || delta.Rows[0].Unit != b || delta.Rows[0].Summary.Reason != "1 open items" || len(delta.Removed) != 0 {
		t.Fatalf("delta after one summary changed: %+v", delta)
	}
	if needs := rows(api.ViewNeedsMe, needsAt); needs.Full || len(needs.Rows) != 1 || needs.Rows[0].Unit != b {
		t.Fatalf("needs-me delta: %+v", needs)
	}

	at2 := api.Since{Epoch: delta.Epoch, Rev: delta.Rev}
	p.drop(a)
	log.Note(port.TopicPRs, a)
	if c := next("a unit that left"); c.All || !slices.Equal(c.Units, []unit.Unit{a}) {
		t.Fatalf("change %+v", c)
	}
	if gone := rows(api.ViewAll, at2); gone.Full || len(gone.Rows) != 0 || !slices.Equal(gone.Removed, []unit.Unit{a}) {
		t.Fatalf("delta after a unit left: %+v", gone)
	}
}

func TestThreadRowNeedsNoPRField(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	th := fakethread.Thread{Key: "C1/171.1", Channel: "general", Title: "deploy?", Kind: fakethread.KindWaiting, WaitsOn: "me", Reason: "ana asked you", Since: at}
	m := readmodel.New(change.NewLog(change.DefaultWindow, nil), nil, nil, nil, nil, fakethread.New(th))
	page, err := m.Rows(context.Background(), api.ViewNeedsMe, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("needs-me %+v", page.Rows)
	}
	r := page.Rows[0]
	if r.Unit != th.Unit() || r.Payload != nil || r.Summary.Line != "ana asked you" || !slices.Equal(r.Facets[fakethread.FacetChannel], []string{"general"}) {
		t.Fatalf("thread row %+v", r)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"payload", "author", "repo", "workspace", "brief", "facts", "override"} {
		if _, ok := top[k]; ok {
			t.Errorf("the thread row encodes %q: %s", k, raw)
		}
	}
	if c := m.Catalog(); len(c.Views) != 0 || len(c.Situations) != 2 || c.Facets[0].Provider != "thread" {
		t.Errorf("catalog %+v", c)
	}
}
