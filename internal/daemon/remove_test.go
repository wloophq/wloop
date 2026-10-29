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

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
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

func TestDoneViewListsFinishedPullRequestsWithAWorkspace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	f.open(30, "fer", at, merged)
	f.open(31, "fer", at, merged)
	f.open(32, "fer", at, nil)
	linkWorkspaces(t, f.st, ref(30), ref(32))
	if got := numbers(f.rows("?view=" + api.ViewDone)); !slices.Equal(got, []int{30}) {
		t.Fatalf("done %v", got)
	}
	for _, v := range []string{api.ViewNeedsMe, api.ViewStalled} {
		if slices.Contains(numbers(f.rows("?view="+v)), 30) {
			t.Fatalf("a done row in %s", v)
		}
	}
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

func TestRemoveDoneRemovesTheCleanOnesAndSaysWhyItKeptTheRest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	f.open(30, "fer", at, merged)
	f.open(31, "fer", at, merged)
	f.open(32, "fer", at, nil)
	linkWorkspaces(t, f.st, ref(30), ref(31), ref(32))
	f.open(33, "fer", at, merged)
	linkWorkspaces(t, f.st, ref(30), ref(31), ref(32), ref(33))
	actions := &fakeActions{refuse: map[github.Ref]string{ref(31): "3 commits not pushed"}, broken: map[github.Ref]error{ref(33): errors.New("orca: worktree rm: timeout")}}
	f.s.deps.Actions = actions
	rec := post(t, f.s, "/api/workspaces/remove-done", `{}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res api.RemoveDoneResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(res.Kept, func(a, b api.Kept) int { return strings.Compare(a.Label, b.Label) })
	if !slices.Equal(res.Removed, []api.Named{{Unit: ref(30).Unit(), Label: "acme/api#30"}}) || len(res.Kept) != 2 || res.Kept[0].Unit != ref(31).Unit() || res.Kept[0].Reason != "3 commits not pushed" ||
		res.Kept[1].Unit != ref(33).Unit() || !strings.Contains(res.Kept[1].Reason, "timeout") {
		t.Fatalf("%+v", res)
	}
	if !slices.Equal(actions.calls, []string{"remove acme/api#30"}) {
		t.Fatalf("calls %v", actions.calls)
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
	s := newServer(t)
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
	rec = post(t, s, "/api/workspaces/remove-done", `{}`)
	var res api.RemoveDoneResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Kept) != 1 || res.Kept[0].Reason != stuck.Reason || res.Kept[0].Detail != stuck.Detail {
		t.Fatalf("%s", rec.Body)
	}
}

func TestForceReachesTheActionAndForceableTheCaller(t *testing.T) {
	t.Parallel()
	s := newServer(t)
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
	rec = post(t, s, "/api/workspaces/remove-done", `{}`)
	var res api.RemoveDoneResult
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
	got := numbers(f.rows("?view=" + api.ViewApproved))
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
