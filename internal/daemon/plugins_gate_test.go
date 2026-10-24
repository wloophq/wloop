package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

func githubOff(p unit.ProviderID) bool { return p != unit.GitHub }

func wantGitHubDisabled(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]string
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["error"] != "github is disabled" || body["plugin"] != "github" {
		t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
	}
}

func TestActionOnDisabledPluginIs409(t *testing.T) {
	actions := &fakeActions{}
	s := newServerWith(t, func(d *Deps) { d.Actions, d.PluginEnabled = actions, githubOff })
	for _, action := range []string{"review", "mute", "remove", "open"} {
		wantGitHubDisabled(t, action, post(t, s, "/api/units/github/acme%2Fapi%237/"+action, `{"duration":"1h"}`))
	}
	if len(actions.calls) != 0 {
		t.Fatalf("an action port was called: %v", actions.calls)
	}
	if code := get(t, s, "/api/units/github/acme%2Fapi%237", nil); code != http.StatusOK {
		t.Fatalf("a read of a disabled plugin's unit: %d", code)
	}
}

// countingExplorer counts the searches it is asked for.
type countingExplorer struct {
	openOnGitHub
	n atomic.Int32
}

func (c *countingExplorer) SearchCandidatesEach(ctx context.Context, qs []string, after string, n int) ([]dgh.CandidatePage, []error, error) {
	c.n.Add(1)
	return c.openOnGitHub.SearchCandidatesEach(ctx, qs, after, n)
}

func TestUnitScopedPostsOnDisabledPluginAre409(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		// run sets a server up with github enabled, disables it, posts and
		// reports whether the route's port was called.
		run func(t *testing.T) (*httptest.ResponseRecorder, bool)
	}{
		{"IntakeWithout", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s, fa := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
			in := ask(t, s, "review", `{}`)
			waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
			s.deps.PluginEnabled = githubOff
			rec := post(t, s, fmt.Sprintf("/api/intakes/%d/without", in.Seq), "")
			return rec, intakeOf(t, s, in.Seq).Brief == intake.BriefWithout || len(fa.calls()) > 0
		}},
		{"IntakeCancel", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
			in := ask(t, s, "review", `{}`)
			waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
			s.deps.PluginEnabled = githubOff
			rec := post(t, s, fmt.Sprintf("/api/intakes/%d/cancel", in.Seq), "")
			return rec, intakeOf(t, s, in.Seq).State == intake.Canceled
		}},
		{"Plan", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			plan := newPendingPlan(agentNow)
			plan.propose(r7.Unit())
			s := newServerWith(t, func(d *Deps) { d.Plan, d.PluginEnabled = plan, githubOff })
			rec := post(t, s, api.Plan, `{"ids":["item-acme/api#7"],"action":"dismiss"}`)
			return rec, len(plan.acted) > 0 || !plan.isPending(r7.Unit())
		}},
		{"AgentTurnsStart", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s, svc := agentServer(t)
			sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
			s.deps.PluginEnabled = githubOff
			rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`)
			return rec, bookOf(t, svc, agentRef).Turns[0].State == turn.Started
		}},
		{"AgentTurnsByHand by url", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			hand := &byHandCalls{}
			s, _ := newIntakeServer(t, nil, func(d *Deps) { d.TurnByHand, d.PluginEnabled = hand.run, githubOff })
			rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsByHand, "term-1", `{"intent":"review","cwd":"/w","url":"`+r7.URL()+`"}`)
			return rec, len(hand.list()) > 0
		}},
		{"AgentTurnsByHand by session", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			hand := &byHandCalls{}
			s, _ := newIntakeServer(t, nil, func(d *Deps) {
				d.TurnByHand, d.PluginEnabled = hand.run, githubOff
				d.ByHandRef = func(context.Context, api.ByHandRequest, string) (unit.Unit, error) { return r7.Unit(), nil }
			})
			rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsByHand, "term-1", `{"intent":"review","cwd":"/w"}`)
			return rec, len(hand.list()) > 0
		}},
		{"AgentSessionsStart", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s, svc := agentServer(t)
			sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
			id := sessionOf(t, svc, "term_1")
			if _, err := svc.Start(ctx, id, "abc123abc123"); err != nil {
				t.Fatal(err)
			}
			s.deps.PluginEnabled = githubOff
			rec := agentCall(t, s.Handler(), "POST", api.AgentSessionsStart, "term_1", `{"agent_session":"sess-1","cwd":"/wt/rev"}`)
			sess, _, err := svc.Registry.Session(ctx, id)
			return rec, sess.Worktree != "" || err != nil
		}},
		{"AgentSessionsPrompt", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s, svc := agentServer(t)
			sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
			id := sessionOf(t, svc, "term_1")
			if _, err := svc.Start(ctx, id, "abc123abc123"); err != nil {
				t.Fatal(err)
			}
			if err := svc.SessionStarted(ctx, turns.SessionStart{Session: id, Cwd: "/wt/rev"}); err != nil {
				t.Fatal(err)
			}
			s.deps.PluginEnabled = githubOff
			rec := agentCall(t, s.Handler(), "POST", api.AgentSessionsPrompt, "term_1", `{"prompt":"go on"}`)
			sess, ok, err := svc.Registry.Session(ctx, id)
			if err != nil || !ok {
				t.Fatalf("session %v %v", ok, err)
			}
			return rec, !sess.PromptSeenAt.IsZero()
		}},
		{"intakes off with github", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			s := newServerWith(t, func(d *Deps) { d.Intakes, d.PluginEnabled = nil, githubOff })
			for _, path := range []string{"/api/intakes/1/without", "/api/intakes/1/cancel"} {
				if rec := post(t, s, path, ""); rec.Code != http.StatusConflict {
					return rec, false
				}
			}
			return post(t, s, "/api/intakes/1/cancel", ""), false
		}},
		{"explore", func(t *testing.T) (*httptest.ResponseRecorder, bool) {
			ex := &countingExplorer{}
			s := newServerWith(t, func(d *Deps) { d.Explorer, d.PluginEnabled = ex, githubOff })
			rec := post(t, s, "/api/explore", `{"query":"is:open"}`)
			return rec, ex.n.Load() > 0
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec, called := c.run(t)
			wantGitHubDisabled(t, c.name, rec)
			if called {
				t.Errorf("%s: the port was called", c.name)
			}
		})
	}
}

func TestIntakesUnavailableWithGitHubOnIs503(t *testing.T) {
	s := newServerWith(t, func(d *Deps) { d.Intakes = nil })
	if rec := post(t, s, "/api/intakes/1/cancel", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRemoveDoneSkipsADisabledPluginsUnits(t *testing.T) {
	for _, enabled := range []func(unit.ProviderID) bool{nil, githubOff} {
		actions := &fakeActions{}
		s := newServerWith(t, func(d *Deps) { d.Actions, d.PluginEnabled = actions, enabled })
		linkWorkspaces(t, s.store(), github.Ref{Owner: "acme", Repo: "api", Number: 8})
		rec := post(t, s, "/api/workspaces/remove-done", `{}`)
		var res api.RemoveDoneResult
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		want := 1
		if enabled != nil {
			want = 0
		}
		if len(res.Removed) != want || len(actions.calls) != want {
			t.Errorf("github enabled %v: %s, calls %v", enabled == nil, rec.Body, actions.calls)
		}
	}
}

var pathParam = regexp.MustCompile(`\{([a-z]+)(\.\.\.)?\}`)

// examplePath fills each parameter of an operation's path.
func examplePath(path string) string {
	return pathParam.ReplaceAllStringFunc(path, func(p string) string {
		switch name := pathParam.FindStringSubmatch(p)[1]; name {
		case "provider":
			return "github"
		case "key":
			return "acme%2Fapi%237"
		default:
			return "x"
		}
	})
}

func TestEveryProviderRouteIsGated(t *testing.T) {
	s := newServerWith(t, func(d *Deps) { d.Actions, d.PluginEnabled = &fakeActions{}, githubOff })
	gated := 0
	for _, op := range api.Operations {
		if op.Method == http.MethodGet || !strings.Contains(op.Path, "{provider}") {
			continue
		}
		gated++
		wantGitHubDisabled(t, op.Pattern(), post(t, s, examplePath(op.Path), `{}`))
	}
	if gated == 0 {
		t.Fatal("no mutating route names a provider")
	}
	called := false
	rts := s.gatePlugins([]route{{"POST /api/later/{provider}/{key}/go", func(http.ResponseWriter, *http.Request) { called = true }}})
	mux := http.NewServeMux()
	mux.HandleFunc(rts[0].pattern, rts[0].handle)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/later/github/acme%2Fapi%237/go", nil))
	wantGitHubDisabled(t, "a later route", rec)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/later/slack/T1%2FC1/go", nil))
	if called != true || rec.Code != http.StatusOK {
		t.Fatalf("an enabled provider's later route: %d, called %v", rec.Code, called)
	}
}
