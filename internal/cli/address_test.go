package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func reviewedAuthorScenario(t *testing.T) (*fake.Manager, *testEnv) {
	t.Helper()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-auth", RepoID: "r1", Path: "/wt/auth", DisplayName: "feat-z", Branch: "feat/z", Status: "in-review", HostID: "local", LinkedPR: 9},
		},
		Sessions: map[string]string{"ws-auth": "term-auth"},
	}
	te := newTestEnv(t, m)
	pr := authorPR(9, "feat/z", "CHANGES_REQUESTED", "hfeat/z")
	pr["url"] = "https://github.com/acme/api/pull/9"
	pr["reviews"] = nodes(
		obj{"id": "old", "state": "COMMENTED", "submittedAt": "2026-10-07T10:00:00Z", "body": "x", "author": obj{"login": "bob"}, "commit": obj{"oid": "older"}, "comments": obj{"totalCount": 1}},
		obj{"id": "cr", "state": "CHANGES_REQUESTED", "submittedAt": "2026-10-08T10:00:00Z", "body": "", "author": obj{"login": "bob"}, "commit": obj{"oid": "hfeat/z"}, "comments": obj{"totalCount": 2}},
	)
	te.gh.author = mustJSON(t, obj{"data": obj{"viewer": obj{"login": "me"}, "open": nodes(pr), "recent": nodes()}})
	te.git.head["/wt/auth"] = "hfeat/z"
	te.git.ancestor["/wt/auth"] = true
	return m, te
}

func TestAddressKeepsTheGuidanceWhenThePromptDoesNotLand(t *testing.T) {
	t.Parallel()
	m, te := reviewedAuthorScenario(t)
	m.NotSubmitted = true
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"}}}}}`
	rec := &memRecorder{}
	te.Rec = rec
	if code := te.run("address", "https://github.com/acme/api/pull/9", "skip", "the nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := rec.overrides[pr.Ref{Owner: "acme", Repo: "api", Number: 9}].NextGuidance; got != "skip the nit" {
		t.Fatalf("guidance lost: %q", got)
	}
}

func addressBasics(t *testing.T, author obj) string {
	t.Helper()
	return mustJSON(t, obj{"data": obj{"pr": obj{"pullRequest": obj{
		"number": 21, "title": "Mine", "state": "OPEN", "headRefName": "feat/mine", "baseRefName": "main", "headRefOid": "c21",
		"repository": obj{"nameWithOwner": "acme/api"}, "author": author,
	}}}})
}

func TestAddressCreatesTheAuthorWorkspaceForMyPR(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.branch["/worktrees/feat/mine"] = "feat/mine"
	te.git.ancestor["/worktrees/feat/mine"] = true
	if code := te.run("address", "https://github.com/acme/api/pull/21", "fix", "the test"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	writes := strings.Join(m.Writes, "\n")
	if !strings.HasPrefix(writes, "create feat/mine\nsession ws-1\nlink ws-1 21\n") {
		t.Fatalf("writes:\n%s", writes)
	}
	if len(m.Prompts) == 0 || m.Prompts[len(m.Prompts)-1].Handle != "term-ws-1" {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
	last := m.Prompts[len(m.Prompts)-1].Text
	if h, _, _, _ := promptOf(t, last); h != "Address round on acme/api#21, asked for by you." || !strings.Contains(turnText(t, te, last), "fix the test") {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
	out := te.out.String()
	created, triage := strings.Index(out, "created feat/mine"), strings.Index(out, "triage in feat/mine")
	if created < 0 || triage < 0 || created > triage {
		t.Fatalf("out: %s", out)
	}
}

func TestTheNotOwnedHintSurvivesTheCatalog(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	if code := te.run("address", "https://github.com/acme/api/pull/21"); code == 0 {
		t.Fatalf("addressed someone else's PR: %s", te.out)
	}
	want := "Not yours: take it over first; run '" + brand.Command + " address --take-over https://github.com/acme/api/pull/21'"
	if !strings.Contains(te.err.String(), want) {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 {
		t.Fatalf("writes %v prompts %+v", m.Writes, m.Prompts)
	}
}

func TestAddressUsesTheExistingAuthorWorkspace(t *testing.T) {
	t.Parallel()
	m, te := reviewedAuthorScenario(t)
	linkAuthorWorkspace(t, te, 9, "ws-auth")
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"User","login":"jo"}}}}}`
	if code := te.run("address", "https://github.com/acme/api/pull/9"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if w := strings.Join(m.Writes, "\n"); strings.Contains(w, "create") {
		t.Fatalf("created a workspace beside the existing one:\n%s", w)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-auth" {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
}

func TestAddressUsesAMainCheckoutOnTheBranch(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/mine", HostID: "local", IsMain: true},
		},
		Sessions: map[string]string{"main-api": "term-main"},
	}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.ancestor["/src/api"] = true
	if code := te.run("address", "https://github.com/acme/api/pull/21"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, w := range m.Writes {
		if strings.HasPrefix(w, "create") || strings.HasPrefix(w, "link") || strings.HasPrefix(w, "rename") {
			t.Fatalf("writes:\n%s", strings.Join(m.Writes, "\n"))
		}
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-main" {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
	if !strings.Contains(te.out.String(), "triage in api") {
		t.Fatalf("out: %s", te.out)
	}
}

func TestAddressDoesNotTakeAMainCheckoutForAForkPR(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/mine", HostID: "local", IsMain: true},
		},
		Sessions: map[string]string{"main-api": "term-main"},
	}
	te := newTestEnv(t, m)
	te.gh.basics = strings.Replace(addressBasics(t, obj{"__typename": "User", "login": "me"}), `"headRefOid"`, `"isCrossRepository":true,"headRefOid"`, 1)
	te.git.ancestor["/worktrees/feat/mine"] = true
	if code := te.run("address", "https://github.com/acme/api/pull/21"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.HasPrefix(strings.Join(m.Writes, "\n"), "create feat/mine\n") {
		t.Fatalf("writes:\n%s", strings.Join(m.Writes, "\n"))
	}
	for _, p := range m.Prompts {
		if p.Handle == "term-main" {
			t.Fatalf("prompted the main checkout: %+v", m.Prompts)
		}
	}
}

func TestAddressRefusesAReleasePRFromTheMainCheckoutsDefaultBranch(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api", DefaultBranch: "feat/mine"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/mine", HostID: "local", IsMain: true},
		},
		Sessions: map[string]string{"main-api": "term-main"},
	}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	if code := te.run("address", "https://github.com/acme/api/pull/21"); code == 0 {
		t.Fatalf("addressed a release PR: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "feat/mine is checked out in /src/api") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 {
		t.Fatalf("writes %v prompts %+v", m.Writes, m.Prompts)
	}
}

func TestAddressDoesNotTakeAMainCheckoutOnSomeoneElsesPR(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/mine", HostID: "local", IsMain: true},
		},
		Sessions: map[string]string{"main-api": "term-main"},
	}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	if code := te.run("address", "https://github.com/acme/api/pull/21"); code == 0 {
		t.Fatalf("addressed a colleague's PR in the main checkout: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "take it over first") {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 {
		t.Fatalf("writes %v prompts %+v", m.Writes, m.Prompts)
	}
}

func TestAddressAsksForTheViewerOnlyForAMainCheckout(t *testing.T) {
	t.Parallel()
	m, te := reviewedAuthorScenario(t)
	linkAuthorWorkspace(t, te, 9, "ws-auth")
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"User","login":"jo"}}}}}`
	te.gh.viewerErr = errors.New("rate limited")
	if code := te.run("address", "https://github.com/acme/api/pull/9"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
}

func TestAddressRecordsTheGuidanceItSends(t *testing.T) {
	t.Parallel()
	_, te := reviewedAuthorScenario(t)
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"}}}}}`
	rec := &memRecorder{}
	te.Rec = rec
	if code := te.run("address", "https://github.com/acme/api/pull/9", "skip", "the nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := guidanceEvents(rec); !slices.Equal(got, []string{"skip the nit"}) {
		t.Fatalf("guidance events %q", got)
	}
}

func TestAddressTakeOverTakesOverFirst(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/theirs", Status: "needs-me", HostID: "local", LinkedPR: 20},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/wt/rev"] = "feat/theirs"
	te.git.ancestor["/wt/rev"] = true
	if code := te.run("address", "https://github.com/acme/api/pull/20", "--take-over", "fix", "the", "nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); !strings.HasPrefix(got, "rename ws-rev feat/theirs\nlink ws-rev 20\n") {
		t.Fatalf("writes:\n%s", got)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if h, _, _, _ := promptOf(t, m.Prompts[0].Text); h != "Take over acme/api#20 and address it, asked for by you." {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	st := te.Store
	if o, _ := st.Override(context.Background(), pr.Ref{Owner: "acme", Repo: "api", Number: 20}); !o.Adopted {
		t.Fatal("not marked as taken over")
	}
}

func TestAddressTakeOverOnAPullRequestAlreadyTakenOverAddresses(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/theirs", Status: "needs-me", HostID: "local", LinkedPR: 20},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/wt/rev"] = "feat/theirs"
	te.git.ancestor["/wt/rev"] = true
	for range 2 {
		if code := te.run("address", "https://github.com/acme/api/pull/20", "--take-over", "fix", "the", "nit"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
	}
	if len(m.Prompts) != 2 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
}

func TestATakeOverOfAPullRequestOwnedAlreadyIsNoFailure(t *testing.T) {
	t.Parallel()
	for refusal, want := range map[string]bool{
		api.BuiltinWhy("take_over"): true,
		api.RefusalNotOffered:       true,
		api.BuiltinWhy("pause"):     false,
		"":                          false,
	} {
		if got := ownedAlready(daemonRefusal{"daemon: the profile's words", refusal}); got != want {
			t.Errorf("%q: %v", refusal, got)
		}
	}
	if ownedAlready(errors.New("daemon: It is yours already")) {
		t.Error("the text alone reads as owned")
	}
}

// daemonRefusal is a daemon refusal with its code.
type daemonRefusal struct{ msg, refusal string }

func (e daemonRefusal) Error() string   { return e.msg }
func (e daemonRefusal) Refusal() string { return e.refusal }

func TestAddressTakeOverThroughTheDaemonTakesOverThenAddresses(t *testing.T) {
	t.Parallel()
	te, calls := daemonForActions(t)
	if code := te.run("address", "https://github.com/acme/api/pull/20", "--take-over", "fix", "the", "nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var actions []string
	for _, c := range calls() {
		if strings.HasPrefix(c, "POST /api/units/") {
			actions = append(actions, c)
		}
	}
	if len(actions) != 2 || !strings.HasPrefix(actions[0], "POST /api/units/github/acme/api#20/take_over") ||
		!strings.HasPrefix(actions[1], "POST /api/units/github/acme/api#20/address") || !strings.Contains(actions[1], `"take_over":true`) || !strings.Contains(actions[1], `"guidance":"fix the nit"`) {
		t.Fatalf("actions %v", actions)
	}
}

func TestAddressTakeOverStopsWhenTheDaemonGoesAwayAfterTakingOver(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/theirs", Status: "needs-me", HostID: "local", LinkedPR: 20},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/wt/rev"] = "feat/theirs"
	te.git.ancestor["/wt/rev"] = true
	te.Socket = filepath.Join(shortDir(t), "daemon.sock")
	ln, err := net.Listen("unix", te.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/take_over") {
			ln.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	if code := te.run("address", "https://github.com/acme/api/pull/20", "--take-over", "fix", "the", "nit"); code != 1 || !strings.Contains(te.err.String(), "launchctl kickstart") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 0 {
		t.Fatalf("an address round went out without the daemon: %+v", m.Prompts)
	}
}

// linkAuthorWorkspace has the inventory link pull request n of someone else
// to the author workspace id, as the sweep does.
func linkAuthorWorkspace(t *testing.T, te *testEnv, n int, id string) {
	t.Helper()
	ctx := context.Background()
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: n}
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: ref, Title: "Add z", Author: "jo", State: "OPEN", URL: ref.URL(), Reasons: []string{"workspace"}}); err != nil {
		t.Fatal(err)
	}
	if err := te.Store.SetWorkspace(ctx, ref, dinv.Workspace{ID: id}); err != nil {
		t.Fatal(err)
	}
}

func TestAnAddressAskedForByHandSaysWhyNow(t *testing.T) {
	t.Parallel()
	for _, synced := range []bool{true, false} {
		m, te := reviewedAuthorScenario(t)
		te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"}}}}}`
		if synced {
			if err := te.syncInventory(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if code := te.run("address", "https://github.com/acme/api/pull/9"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if len(m.Prompts) != 1 {
			t.Fatalf("prompts %+v", m.Prompts)
		}
		text := turnText(t, te, m.Prompts[0].Text)
		if got := strings.Contains(text, "## Why now") && strings.Contains(text, "- @bob\n"); got != synced {
			t.Fatalf("synced %v, why now %v:\n%s", synced, got, text)
		}
	}
}
