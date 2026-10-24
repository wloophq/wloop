package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
)

var updateWire = flag.Bool("update-wire", false, "rewrite the golden wire files")

type wireBriefs struct{ stubBriefs }

func (wireBriefs) Status(context.Context) (brief.Status, error) {
	return brief.Status{Ready: 2, Pending: 1, Failed: 1, LastError: "claude timed out", PausedUntil: time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)}, nil
}

func (wireBriefs) Views(_ context.Context, prs ...dinv.PR) (map[github.Ref]*brief.View, error) {
	return viewsOf(&brief.View{State: "ready", Model: "haiku", At: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Stale: true,
		Brief: &brief.Brief{Goal: "Export in batches", Context: "exports time out", Kind: "feature", Size: "m", Risk: "low",
			RiskWhy: "behind a flag", OpenQuestions: []string{"batch size?"}}}, prs), nil
}

func (wireBriefs) Briefed(context.Context) (map[github.Ref]brief.Summary, map[github.Ref]dinv.BriefRecord, error) {
	return map[github.Ref]brief.Summary{{Owner: "acme", Repo: "api", Number: 7}: {Size: "m", Risk: "low", Goal: "Export in batches", OpenQuestions: 1}}, nil, nil
}

// TestWireShape pins the JSON the clients and app.js read: a row, a detail
// and health, against the golden files the daemon's own types produced.
func TestWireShape(t *testing.T) {
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	books := turnstest.NewStore()
	books.Put(turn.Book{Unit: pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit(),
		Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, Head: "h2", SentTo: "term-1", Session: "term-1",
			State: turn.Asked, SentAt: at, StartedAt: at}},
		Records: []turn.Record{{Session: "term-1", Head: "h2", Hash: "x", At: at, Record: record.Record{State: record.Asked, Summary: "publish?",
			Outcome:  &record.Outcome{Verdict: "APPROVE", NonBlocking: 1},
			Question: &record.Question{Kind: "confirm", Text: "Publish the approval?", Recommend: "yes", Publishes: true}}}},
		Answers: []turn.Answer{{Text: "wait", Source: turn.Cockpit, Route: turn.Refused, At: at.Add(time.Minute)}}})
	s := newServerWith(t, func(d *Deps) {
		withBriefs(d, wireBriefs{})
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
	})
	ctx := context.Background()
	st := s.store()
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := st.SetWorkspace(ctx, r7, dinv.Workspace{ID: "w1", DisplayName: "REVIEW batch", Status: "reviewing", IsReview: true,
		AgentStates: []turn.AgentState{turn.AgentWorking}, Comment: "r2 held"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOverride(ctx, dinv.Override{PR: r7, Hold: true, Tracked: true, NextGuidance: "look at the tests",
		MutedUntil: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	s.app.Set(api.AppStatus{State: "running", Notifications: "denied"})
	for _, c := range []struct{ path, golden string }{
		{api.Rows, "rows.json"},
		{api.UnitPath(r7.Unit()), "detail.json"},
		{"/api/health", "health.json"},
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", c.path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
		var v any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if c.golden == "rows.json" {
			v = v.(map[string]any)["rows"]
		}
		if c.golden == "health.json" {
			v.(map[string]any)["build"] = "<build>"
		}
		got, _ := json.MarshalIndent(v, "", "  ")
		path := filepath.Join("testdata", "wire", c.golden)
		if *updateWire {
			brandtest.CanUpdate(t)
			if err := os.WriteFile(path, append([]byte(brandtest.Collapse(string(got))), '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(append(got, '\n'), []byte(brandtest.Expand(string(want)))) {
			t.Errorf("%s changed shape:\n%s\nwant:\n%s", c.path, got, want)
		}
	}
}

// TestWireTypesCoverTheirSources fails when a field is added to a type the
// daemon maps without adding it to its wire type.
func TestWireTypesCoverTheirSources(t *testing.T) {
	unwired := map[reflect.Type]int{reflect.TypeFor[turn.Turn](): 1, reflect.TypeFor[dinv.Override](): 1}
	for _, c := range []struct{ from, to any }{
		{dinv.Workspace{}, api.Workspace{}},
		{dinv.Override{}, api.Override{}},
		{dinv.Event{}, api.Event{}},
		{round.Pending{}, api.PendingRound{}},
		{brief.Summary{}, api.BriefSummary{}},
		{brief.View{}, api.BriefView{}},
		{brief.Brief{}, api.Brief{}},
		{brief.Status{}, api.BriefStatus{}},
		{dgh.RateLimit{}, api.RateLimit{}},
		{turn.Turn{}, api.Turn{}},
		{turn.Record{}, api.TurnRecord{}},
		{turn.Answer{}, api.TurnAnswer{}},
	} {
		from, to := reflect.TypeOf(c.from), reflect.TypeOf(c.to)
		if from.NumField() != to.NumField()+unwired[from] {
			t.Errorf("%s has %d fields, %s %d", from, from.NumField(), to, to.NumField())
		}
	}
	cand := reflect.TypeOf(dgh.Candidate{})
	if got := reflect.TypeOf(api.Candidate{}).NumField(); got != cand.NumField()+2 {
		t.Errorf("api.Candidate has %d fields, github.Candidate %d plus the brief and the actions", got, cand.NumField())
	}
}

// kindConsts reads the Kind constants a package declares, by name.
func kindConsts(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			v := spec.(*ast.ValueSpec)
			if id, ok := v.Type.(*ast.Ident); !ok || id.Name != "Kind" {
				continue
			}
			for i, name := range v.Names {
				out[name.Name] = v.Values[i].(*ast.BasicLit).Value
			}
		}
	}
	return out
}

func TestWireKindsAreTheSituationKinds(t *testing.T) {
	sit := kindConsts(t, filepath.Join("..", "situation", "situation.go"))
	served := map[string]bool{}
	for _, s := range newServer(t).readModel().Catalog().Situations {
		served[strconv.Quote(string(s.Kind))] = s.Provider == "github"
	}
	if len(sit) == 0 || len(sit) != len(served) {
		t.Fatalf("situation declares %v, the catalog serves %v", sit, served)
	}
	for name, v := range sit {
		if !served[v] {
			t.Errorf("%s %s is not a served GitHub situation", name, v)
		}
	}
}
