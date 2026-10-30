package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel/prrow"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

func merged(pr *dinv.PR) { pr.State = "MERGED" }

func linkWorkspaces(t *testing.T, st *inventory.Store, refs ...github.Ref) {
	t.Helper()
	links := map[github.Ref]dinv.Workspace{}
	for _, r := range refs {
		links[r] = dinv.Workspace{ID: "ws-" + r.String(), DisplayName: "feat " + r.String(), Status: "completed"}
	}
	if err := inventorytest.Of(st).ReplaceWorkspaces(context.Background(), links); err != nil {
		t.Fatal(err)
	}
}

func TestTheWorkspaceSignalSaysAWorkspaceRemainsWhateverThePullRequestsState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	f.open(30, "fer", at, merged)
	f.open(31, "fer", at, merged)
	f.open(32, "fer", at, nil)
	linkWorkspaces(t, f.st, ref(30), ref(32))
	var got []int
	for _, r := range f.rows("") {
		if prrow.Signals(r)["workspace"] {
			got = append(got, rowNumber(r))
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{30, 32}) {
		t.Fatalf("workspace on %v", got)
	}
}

// cleanRig is a tasks rig whose Done, which offers to remove clean workspaces, lists every done task, with a
// done task holding each of refs, each a merged pull request.
func cleanRig(t *testing.T, edit func(*Deps), refs ...github.Ref) *tasksRig {
	t.Helper()
	l := *bundledLoaded()
	l.Views.Views = slices.Clone(l.Views.Views)
	logbook, _ := l.Views.View("logbook")
	for i := range l.Views.Views {
		if l.Views.Views[i].ID == "done" {
			l.Views.Views[i].When = logbook.When
		}
	}
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded { return &l }
		if edit != nil {
			edit(d)
		}
	})
	for _, ref := range refs {
		if err := inventorytest.Of(r.s.store()).UpsertPR(context.Background(), dinv.PR{Ref: ref, Title: "PR " + ref.String(), Author: "me", State: "MERGED"}); err != nil {
			t.Fatal(err)
		}
		r.put(t, task.Task{ID: task.ID("t-" + strconv.Itoa(ref.Number)), State: "done", Category: task.CategoryDone, Units: []unit.Unit{ref.Unit()}})
	}
	return r
}

func removeClean(t *testing.T, s *Server, body string) (int, api.RemoveCleanResult) {
	t.Helper()
	rec := post(t, s, "/api/workspaces/remove-clean", body)
	var res api.RemoveCleanResult
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, res
}

func TestRemoveActionRecordsTheEvent(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	actions := &fakeActions{}
	s.deps.Actions = actions
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	linkWorkspaces(t, s.store(), r8)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%238/remove", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(actions.calls, []string{"remove acme/api#8"}) || removedEvents(t, s, r8) != 1 {
		t.Fatalf("calls %v", actions.calls)
	}
}

func TestARefusedRemoveIs409WithTheReason(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	s.deps.Actions = &fakeActions{refuse: map[github.Ref]string{r8: "2 uncommitted files"}}
	linkWorkspaces(t, s.store(), r8)
	rec := post(t, s, "/api/units/github/acme%2Fapi%238/remove", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "2 uncommitted files") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if removedEvents(t, s, r8) != 0 {
		t.Fatal("recorded a removal that did not happen")
	}
}

func TestRemoveCleanRemovesTheCleanOnesOfAViewsTasksAndSaysWhyItKeptTheRest(t *testing.T) {
	t.Parallel()
	actions := &fakeActions{refuse: map[github.Ref]string{ref(31): "3 commits not pushed"}, broken: map[github.Ref]error{ref(33): errors.New("orca: worktree rm: timeout")}}
	r := cleanRig(t, func(d *Deps) { d.Actions = actions }, ref(30), ref(31), ref(33), ref(34))
	if err := inventorytest.Of(r.s.store()).UpsertPR(context.Background(), dinv.PR{Ref: ref(32), Title: "PR 32", Author: "me", State: "OPEN"}); err != nil {
		t.Fatal(err)
	}
	r.put(t, task.Task{ID: "t-32", State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(32).Unit()}})
	linkWorkspaces(t, r.s.store(), ref(30), ref(31), ref(32), ref(33))
	code, res := removeClean(t, r.s, `{"view":"done"}`)
	if code != 200 {
		t.Fatalf("%d", code)
	}
	slices.SortFunc(res.Kept, func(a, b api.Kept) int { return strings.Compare(a.Label, b.Label) })
	if !slices.Equal(res.Removed, []api.Named{{Unit: ref(30).Unit(), Label: "acme/api#30"}}) || len(res.Kept) != 2 || res.Kept[0].Unit != ref(31).Unit() || res.Kept[0].Reason != "3 commits not pushed" ||
		res.Kept[1].Unit != ref(33).Unit() || !strings.Contains(res.Kept[1].Reason, "timeout") {
		t.Fatalf("%+v", res)
	}
	if !slices.Equal(actions.calls, []string{"remove acme/api#30"}) {
		t.Fatalf("calls %v", actions.calls)
	}
	for _, body := range []string{`{"view":"logbook"}`, `{"view":"nowhere"}`, `{}`} {
		if code, _ := removeClean(t, r.s, body); code != 400 {
			t.Errorf("%s: %d, want 400 for a view that does not offer it", body, code)
		}
	}
}

func removedEvents(t *testing.T, s *Server, ref github.Ref) int {
	t.Helper()
	evs, err := s.store().Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == dinv.KindRemoved {
			n++
		}
	}
	return n
}

func TestARefusalCarriesTheToolsOwnMessage(t *testing.T) {
	t.Parallel()
	s := cleanRig(t, nil, github.Ref{Owner: "acme", Repo: "api", Number: 8}).s
	var logged strings.Builder
	s.deps.Log = func(format string, args ...any) { fmt.Fprintf(&logged, format+"\n", args...) }
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	stuck := Refusal{Reason: "its terminals could not be stopped", Detail: "runtime_error: Failed to physically stop every PTY"}
	s.deps.Actions = &fakeActions{broken: map[github.Ref]error{r8: stuck}}
	linkWorkspaces(t, s.store(), r8)
	rec := post(t, s, "/api/units/github/acme%2Fapi%238/remove", `{}`)
	var body struct{ Error, Detail string }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 409 || body.Error != stuck.Reason || body.Detail != stuck.Detail {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logged.String(), stuck.Detail) {
		t.Fatalf("log %q", logged.String())
	}
	rec = post(t, s, "/api/workspaces/remove-clean", `{"view":"done"}`)
	var res api.RemoveCleanResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Kept) != 1 || res.Kept[0].Reason != stuck.Reason || res.Kept[0].Detail != stuck.Detail {
		t.Fatalf("%s", rec.Body)
	}
}

func TestForceReachesTheActionAndForceableTheCaller(t *testing.T) {
	t.Parallel()
	s := cleanRig(t, nil, github.Ref{Owner: "acme", Repo: "api", Number: 8}).s
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	actions := &fakeActions{broken: map[github.Ref]error{r8: Refusal{Reason: "its terminals could not be stopped", Detail: "PTY", Forceable: true}}}
	s.deps.Actions = actions
	linkWorkspaces(t, s.store(), r8)
	rec := post(t, s, "/api/units/github/acme%2Fapi%238/remove", `{}`)
	var body struct {
		Error     string
		Forceable bool
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 409 || !body.Forceable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	post(t, s, "/api/units/github/acme%2Fapi%238/remove", `{"force":true}`)
	rec = post(t, s, "/api/workspaces/remove-clean", `{"view":"done"}`)
	var res api.RemoveCleanResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !slices.Equal(actions.forced, []bool{false, true, false}) {
		t.Fatalf("forced %v", actions.forced)
	}
	if len(res.Kept) != 1 || !res.Kept[0].Forceable {
		t.Fatalf("%s", rec.Body)
	}
}

func TestApprovedViewHoldsOpenPullRequestsThatWaitOnNothingOfMine(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	review := func(n int, by, state string) dinv.Event {
		return dinv.Event{PR: ref(n), Kind: dinv.KindReview, Key: by + state, Actor: by, At: at.Add(time.Minute), Data: state + ":h" + strconv.Itoa(n)}
	}
	f.open(40, "ana", at, nil, review(40, "fer", "APPROVED"))
	f.open(41, "ana", at, nil, dinv.Event{PR: ref(41), Kind: dinv.KindReview, Key: "old", Actor: "fer", At: at.Add(-time.Minute), Data: "APPROVED:old"})
	f.open(42, "fer", at, nil, review(42, "ana", "APPROVED"))
	f.open(43, "fer", at, nil)
	f.open(44, "ana", at, nil, review(44, "fer", "APPROVED"))
	f.open(45, "fer", at, nil, review(45, "ana", "APPROVED"))
	links := map[github.Ref]dinv.Workspace{ref(45): {ID: "main-45", DisplayName: "api", IsMain: true}}
	for _, n := range []int{40, 41, 43} {
		links[ref(n)] = dinv.Workspace{ID: fmt.Sprint("ws-", n), DisplayName: fmt.Sprint("REVIEW ", n), IsReview: n != 43}
	}
	links[ref(42)] = dinv.Workspace{ID: "ws-42", DisplayName: "feat 42"}
	if err := inventorytest.Of(f.st).ReplaceWorkspaces(context.Background(), links); err != nil {
		t.Fatal(err)
	}
	got := numbers(f.rows("?is=approved"))
	slices.Sort(got)
	if !slices.Equal(got, []int{40, 42}) {
		t.Fatalf("approved %v", got)
	}
}

func TestForcedRemoveWithoutAStoredLinkLetsTheActionFindTheWorkspace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.open(30, "fer", fixtureNow.Add(-time.Hour), merged)
	actions := &fakeActions{}
	f.s.deps.Actions = actions
	if rec := post(t, f.s, "/api/units/github/acme%2Fapi%2330/remove", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no remove on acme/api#30") {
		t.Fatalf("unforced: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, f.s, "/api/units/github/acme%2Fapi%2330/remove", `{"force":true}`); rec.Code != 200 {
		t.Fatalf("forced: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(actions.calls, []string{"remove acme/api#30"}) || !slices.Equal(actions.ids, []string{""}) || !slices.Equal(actions.forced, []bool{true}) {
		t.Fatalf("calls %v ids %q forced %v", actions.calls, actions.ids, actions.forced)
	}
}
