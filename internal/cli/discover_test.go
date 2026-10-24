package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func requestedPR(number int, branch, owner string, fork bool) obj {
	pr := reviewerPR(number, "OPEN", nil, nil)["pullRequest"].(obj)
	pr["headRefName"] = branch
	pr["isCrossRepository"] = fork
	pr["repository"] = obj{"nameWithOwner": owner + "/api"}
	return pr
}

func discoverScenario(t *testing.T, requested ...obj) (*fake.Manager, *testEnv) {
	t.Helper()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", Status: "todo", HostID: "local", LinkedPR: 7},
		},
	}
	te := newTestEnv(t, m)
	te.gh.reviewer = mustJSON(t, obj{"data": obj{"viewer": obj{"login": "me"}, "pr0": reviewerPR(7, "OPEN", nil, nil)}})
	te.gh.requested = mustJSON(t, obj{"data": obj{"viewer": obj{"login": "me"}, "requested": nodes(requested...)}})
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":20,"title":"New","state":"OPEN","headRefName":"feat/new","headRefOid":"c9","repository":{"nameWithOwner":"acme/api"}}}}}`
	return m, te
}

// firstRoundCase is a review request on acme/api#20 with no workspace.
func firstRoundCase(t *testing.T) sweepCase {
	t.Helper()
	sc := asSweep(discoverScenario(t, requestedPR(20, "feat/new", "acme", false)))
	sc.ref = github.Ref{Owner: "acme", Repo: "api", Number: 20}
	return sc
}

func TestAFirstRoundCreatesItsReviewWorkspace(t *testing.T) {
	sc := firstRoundCase(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFirst)
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(sc.ws.Writes, "\n"); !strings.Contains(w, "create REVIEW feat/new") || len(sc.ws.Prompts) != 1 {
		t.Fatalf("writes:\n%s\nprompts %+v", w, sc.ws.Prompts)
	}
	if h, _, _, _ := promptOf(t, sc.ws.Prompts[0].Text); h != "First review of acme/api#20, started by "+brand.Name+"." {
		t.Fatalf("prompt = %q", sc.ws.Prompts[0].Text)
	}
	if !slices.Contains(r.rec.kinds(), dinv.KindRoundSent) {
		t.Fatalf("events %v", r.rec.kinds())
	}
}

func TestAFirstRoundWhoseSubmissionWasNotObservedRecordsNoRound(t *testing.T) {
	sc := firstRoundCase(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFirst)
	sc.ws.NotSubmitted = true
	if _, err := r.run(t, automation.RunOpts{}); err == nil || !strings.Contains(err.Error(), "not observed") {
		t.Fatalf("run: %v", err)
	}
	if slices.Contains(r.rec.kinds(), dinv.KindRoundSent) {
		t.Fatalf("events %v", r.rec.kinds())
	}
}

func TestAFollowUpWhoseSubmissionWasNotObservedStoresNothing(t *testing.T) {
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	sc.ws.NotSubmitted = true
	if _, err := r.run(t, automation.RunOpts{}); err == nil || !strings.Contains(err.Error(), "not observed") {
		t.Fatalf("run: %v", err)
	}
	st, _ := sc.te.Rounds.Load(context.Background())
	if st.Fingerprint(sc.ref.String()) != "" {
		t.Fatal("fingerprint stored without an observed submission")
	}
	if st.CapReason(sc.ref.String(), testNow, 1) != "" {
		t.Fatal("unobserved submission counted against the cap")
	}
}

func TestAnAddressRoundQueuesInABusySession(t *testing.T) {
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), roundAddress)
	sc.ws.Activity = map[string]wsdomain.Activity{"ws-auth": {Busy: true, UserActive: true}}
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(sc.ws.Writes, "\n"); !strings.Contains(w, "queue term-auth") {
		t.Fatalf("writes:\n%s", w)
	}
	st, _ := sc.te.Rounds.Load(context.Background())
	if st.Fingerprint(sc.ref.String()) == "" {
		t.Fatal("a queued prompt was not recorded; the next sweep would send it again")
	}
}

func TestAnAddressRoundStartsNoSessionBesideABusyOneWithoutTheTool(t *testing.T) {
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), roundAddress)
	sc.ws.Activity = map[string]wsdomain.Activity{"ws-auth": {Busy: true}}
	sc.te.reg.withoutMCP("term-auth")
	_, _ = r.run(t, automation.RunOpts{})
	if w := strings.Join(sc.ws.Writes, "\n"); strings.Contains(w, "session ws-auth") || len(sc.ws.Prompts) != 0 {
		t.Fatalf("writes:\n%s\nprompts %+v", w, sc.ws.Prompts)
	}
}
