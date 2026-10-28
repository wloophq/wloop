package github

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// waitingOnBob stores ana's pull request 7, whose review she asked of bob
// ten days before testAt, with the host holding the request's event.
func waitingOnBob(t *testing.T, p *Plugin, h *fakeHost) {
	t.Helper()
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	asked := testAt.Add(-10 * 24 * time.Hour)
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, Title: "Change 7", Author: "ana", State: pr.StateOpen, HeadOid: "h7", URL: ref7.URL(),
		CreatedAt: asked, ReviewRequests: []string{"bob"}, Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.events = append(h.events, src.Event{Unit: ref7.Unit(), Kind: pr.KindReviewRequest, Key: "rr", Actor: "ana", At: asked, Data: json.RawMessage("bob")})
	h.mu.Unlock()
}

func TestThePluginProposesTheNudgeOfItsStalledPullRequests(t *testing.T) {
	t.Parallel()
	h := &fakeHost{}
	p := started(t, &githubGraphQL{states: map[int]string{}}, h)
	waitingOnBob(t, p, h)
	ctx := context.Background()
	ds, err := p.Proposals(ctx, testAt)
	if err != nil || len(ds) != 1 {
		t.Fatalf("proposals %+v %v", ds, err)
	}
	if d := ds[0]; d.Unit != ref7.Unit() || d.Origin.Rule != nudge.Rule || !strings.Contains(d.Text, "@{github:bob}") || d.Delivery != "" {
		t.Fatalf("proposed %+v", d)
	}
	if ds, err := p.Proposals(ctx, testAt.Add(-9*24*time.Hour)); err != nil || len(ds) != 0 {
		t.Fatalf("a day after the request: %+v %v", ds, err)
	}
}

func TestTheNudgePrepareAsksWhomTheHostsUnmutedRowWaitsOn(t *testing.T) {
	t.Parallel()
	h := &fakeHost{}
	p := started(t, &githubGraphQL{states: map[int]string{}}, h)
	waitingOnBob(t, p, h)
	ctx := context.Background()
	since := testAt.Add(-10 * 24 * time.Hour)
	args, _ := json.Marshal(map[string]any{"unmuted": map[string]any{"kind": "review", "summary": map[string]any{"waits_on": "bob", "since": since, "reason": "review requested"},
		"payload": map[string]any{"author": "ana"}}, "inputs": map[string]any{"guidance": "be kind"}})
	raw, err := p.Action(ctx, src.ActionRequest{Unit: ref7.Unit(), Action: ActionNudgePrepare, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Inputs struct {
			Who         []string `json:"who"`
			Situation   string   `json:"situation"`
			Maintainers bool     `json:"waits_on_maintainers"`
			Guidance    string   `json:"guidance"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || !slices.Equal(res.Inputs.Who, []string{"@bob"}) || res.Inputs.Guidance != "be kind" ||
		res.Inputs.Maintainers || !strings.HasPrefix(res.Inputs.Situation, "Waiting on: @bob since ") {
		t.Fatalf("prepared %s %v", raw, err)
	}
	h.mu.Lock()
	h.events = append(h.events, src.Event{Unit: ref7.Unit(), Kind: pr.KindNudged, At: testAt.Add(-time.Hour), Data: json.RawMessage("@bob")})
	h.mu.Unlock()
	if _, err := p.Action(ctx, src.ActionRequest{Unit: ref7.Unit(), Action: ActionNudgePrepare, Args: args}); !errors.Is(err, src.ErrUnfit) || !strings.Contains(err.Error(), "already nudged @bob") {
		t.Fatalf("a nudge within the window: %v", err)
	}
	if _, err := p.Action(ctx, src.ActionRequest{Unit: ref7.Unit(), Action: ActionNudgePrepare}); !errors.Is(err, src.ErrNotFound) {
		t.Fatalf("no row: %v", err)
	}
}

func TestADraftsNudgedIsTheOnePublisherRecorded(t *testing.T) {
	t.Parallel()
	p := &Plugin{}
	d := src.Draft{Effects: []string{nudge.EffectNudged}}
	if err := p.ApplyEffect(context.Background(), src.EffectRequest{Effect: nudge.EffectNudged, Draft: &d}); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyEffect(context.Background(), src.EffectRequest{Effect: nudge.EffectNudged, Turn: &src.TurnRef{ID: "t1"}}); !errors.Is(err, src.ErrUnsupported) {
		t.Fatalf("a turn's pr.nudged: %v", err)
	}
}
