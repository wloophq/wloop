package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/pb"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

var (
	t0  = time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)
	pr  = plugin.Unit{Provider: "pulls", Key: "acme/api#12"}
	ana = source.Person{Key: "pulls:ana", Name: "Ana", Aliases: []string{"email:ana@acme.example"}}
	bru = source.Person{Key: "pulls:bruno", Name: "Bruno"}
)

// recorder is a host that records what reached it.
type recorder struct {
	mu        sync.Mutex
	snapshots []source.Snapshot
	sessions  []string
	asks      []source.AskRequest
	ops       []source.ItemOp
}

func (r *recorder) Report(_ context.Context, s []source.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots = append(r.snapshots, s...)
	return nil
}

func (r *recorder) Changed(context.Context, []plugin.Unit, []plugin.Topic) error { return nil }

func (r *recorder) ApplyItems(_ context.Context, ops []source.ItemOp) ([]source.ItemResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, ops...)
	out := make([]source.ItemResult, len(ops))
	for i, o := range ops {
		out[i] = source.ItemResult{Item: "i" + o.Op}
	}
	return out, nil
}

func (r *recorder) QueryItems(context.Context, source.ItemQuery) ([]source.Item, error) {
	return nil, nil
}

func (r *recorder) Ask(_ context.Context, req source.AskRequest) (source.AskResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asks = append(r.asks, req)
	return source.AskResult{Text: "yes", CostUSD: 0.02}, nil
}

func (r *recorder) Meter(context.Context, source.Sample) error { return nil }

func (r *recorder) Config(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"enabled":true}`), nil
}

type sessions struct{ rec *recorder }

func (s sessions) Tasks(context.Context, []plugin.Unit) ([]source.Task, error) { return nil, nil }
func (s sessions) Books(context.Context, []plugin.Unit) ([]source.Book, error) { return nil, nil }
func (s sessions) RoundState(context.Context, plugin.Unit) (source.RoundState, error) {
	return source.RoundState{}, nil
}
func (s sessions) Workspaces(context.Context, string) ([]source.Workspace, error) { return nil, nil }

func (s sessions) Session(_ context.Context, handle string) (source.Session, error) {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.sessions = append(s.rec.sessions, handle)
	return source.Session{Unit: pr, Turn: "t-1", TurnSeq: 7, Intent: "review", AllowedEffects: []string{"pull.nudged"}}, nil
}

func (r *recorder) host() source.Host {
	return source.Host{Reporter: r, Ledger: r, Reader: sessions{r}, Services: r}
}

func snapshot() source.Snapshot {
	anchor := source.Anchor{Unit: pr, Ref: "review:9", Permalink: "https://pulls.example/acme/api/pull/12#r9", At: t0}
	return source.Snapshot{
		Unit: pr, Rev: 42, At: t0,
		Data:      json.RawMessage(`{"title":"Add retries","draft":false}`),
		Situation: json.RawMessage(`{"kind":"to_review"}`),
		Card: &source.Card{
			Label: "api#12", Title: "Add retries", State: "open", Outcome: "",
			Signals:   []source.CardSignal{{Key: "checks", Value: "green", Tone: "good", At: t0}},
			LastHuman: &source.Human{Who: ana, What: "commented", At: t0, Link: "https://pulls.example/c/1"},
			Why:       "Ana asked for your review", WaitsOn: []source.Person{bru}, Since: t0,
			Link: "https://pulls.example/acme/api/pull/12", TurnsLink: "/turns/12", StartedByMe: true, ClosurePosted: true, Cares: true,
		},
		Items: []source.Item{{
			ID: "pull:review", Unit: pr, Kind: "request", Summary: "Review the retries", Context: "Ana asked",
			Options: []source.Option{{Label: "approve", ArguedBy: []source.Person{ana}}}, Deadline: "2026-03-04",
			URLs: []string{"https://pulls.example/acme/api/pull/12"}, Contexts: []string{"@repo-api"}, Category: "ready",
			Origin: anchor, Requester: ana, Assignees: []source.Person{bru}, AssignedAt: anchor, Basis: "explicit_mention",
			WaitingOn: []source.Person{bru}, State: "open", Resolution: "done",
			Evidence: source.ItemEvidence{Kind: "message", Anchor: anchor}, Trail: []source.ItemEvidence{{Kind: "unit_event", Anchor: anchor}},
			Confidence: "high", Ambiguity: "none", Provenance: "derived", Confirmed: true, TaskKey: pr.Key,
			UserAt: t0, CreatedAt: t0, UpdatedAt: t0.Add(time.Hour),
		}},
		Facts:      []source.Fact{{Name: "pull.state", Value: source.String("OPEN")}, {Name: "pull.waiting", Value: source.Since(t0)}},
		Signals:    []source.Signal{{Name: "stalled", On: true, Since: t0}, {Name: "ready"}},
		Owes:       []source.Person{bru},
		Offers:     []source.Offer{{Action: "pull.merge", Args: json.RawMessage(`{"method":"squash"}`)}},
		Extraction: &source.ExtractionHealth{Pending: 3, BudgetHit: true, Oldest: t0},
		Contexts:   []string{"@repo-api"},
		Due:        t0.Add(48 * time.Hour),
	}
}

func TestSourceRoundTripsASnapshot(t *testing.T) {
	rec := &recorder{}
	want := []source.Snapshot{snapshot(), {Unit: plugin.Unit{Provider: "pulls", Key: "acme/api#13"}, Rev: 1, Gone: true}}
	c := sourcetest.Launch(t, "pulls", sourcetest.NewStub(want...), rec.host())
	res, err := c.Ingest(t.Context(), source.IngestRequest{Job: "ingest", Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "reported" {
		t.Errorf("summary %q", res.Summary)
	}
	rec.mu.Lock()
	got := rec.snapshots
	rec.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the host got\n%+v\nwant\n%+v", got, want)
	}
}

// tooler is a source whose tool calls back the host.
type tooler struct{ *sourcetest.Stub }

func (s tooler) CallTool(ctx context.Context, call source.ToolCall) (source.ToolResult, error) {
	h := s.Host()
	se, err := h.Session(ctx, call.Session)
	if err != nil {
		return source.ToolResult{}, err
	}
	ans, err := h.Ask(ctx, source.AskRequest{Prompt: "Is it done?", Tier: "small", MaxCostUSD: 0.5, MeterScope: source.CallOf(ctx).MeterScope + "/ask"})
	if err != nil {
		return source.ToolResult{}, err
	}
	res, err := h.ApplyItems(ctx, []source.ItemOp{{Op: "create", Unit: se.Unit, Anchor: source.Anchor{Unit: se.Unit, Ref: "turn:" + call.Turn},
		Fields: &source.ItemFields{Kind: ptr("todo"), Summary: ptr("Answer Ana"), WaitingOn: &[]source.Person{ana}}}})
	if err != nil {
		return source.ToolResult{}, err
	}
	wakeErr := h.SetWake(ctx, source.Wake{Unit: se.Unit, Until: t0})
	out, _ := json.Marshal(map[string]any{
		"effects": se.AllowedEffects, "answer": ans.Text, "item": res[0].Item,
		"priority": source.CallOf(ctx).Priority, "wake": errors.Is(wakeErr, source.ErrUnsupported),
	})
	return source.ToolResult{Result: out}, nil
}

func ptr[T any](v T) *T { return &v }

func TestBrokerCallbackReachesTheHost(t *testing.T) {
	rec := &recorder{}
	c := sourcetest.Launch(t, "pulls", tooler{sourcetest.NewStub()}, rec.host())
	ctx := source.WithCall(t.Context(), source.Call{Priority: source.PriorityBackground, MeterScope: "mcp/pull_read"})
	res, err := c.CallTool(ctx, source.ToolCall{Tool: "pull_read", Args: json.RawMessage(`{}`), Session: "s-1", Turn: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Result, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"effects": []any{"pull.nudged"}, "answer": "yes", "item": "icreate", "priority": float64(source.PriorityBackground), "wake": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tool result %v, want %v", got, want)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !slices.Equal(rec.sessions, []string{"s-1"}) {
		t.Errorf("sessions read %v", rec.sessions)
	}
	if !reflect.DeepEqual(rec.asks, []source.AskRequest{{Prompt: "Is it done?", Tier: "small", MaxCostUSD: 0.5, MeterScope: "mcp/pull_read/ask"}}) {
		t.Errorf("asks %+v", rec.asks)
	}
	if len(rec.ops) != 1 || *rec.ops[0].Fields.Summary != "Answer Ana" || !reflect.DeepEqual(*rec.ops[0].Fields.WaitingOn, []source.Person{ana}) ||
		rec.ops[0].Fields.Context != nil || rec.ops[0].Fields.URLs != nil {
		t.Errorf("ops %+v", rec.ops)
	}

	caps, err := c.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(caps, []source.Capability{source.CapabilityIngester, source.CapabilityTooler}) {
		t.Errorf("capabilities %v", caps)
	}
	if _, err := c.Publish(t.Context(), source.Draft{Unit: pr}); !errors.Is(err, source.ErrUnsupported) {
		t.Errorf("Publish on a plugin with no publisher = %v", err)
	}
}

// planner reports facts of every kind through Plan.
type planner struct {
	*sourcetest.Stub
	facts []source.Fact
	got   chan source.RoundState
}

func (p planner) Plan(_ context.Context, _ plugin.Unit, st source.RoundState) (source.Plan, error) {
	p.got <- st
	return source.Plan{Facts: p.facts, Due: t0.Add(time.Hour), Rank: 3}, nil
}

func (planner) Payload(context.Context, source.PayloadRequest) (json.RawMessage, error) {
	return nil, nil
}
func (planner) ApplyEffect(context.Context, source.EffectRequest) error { return nil }
func (planner) Proposals(context.Context, time.Time) ([]source.Draft, error) {
	return nil, nil
}
func (planner) PlacementDefault(context.Context, plugin.Unit, string) (source.Placement, bool, error) {
	return source.Placement{}, false, nil
}

func TestTemporalFactsRoundTrip(t *testing.T) {
	facts := []source.Fact{
		{Name: "pull.draft", Value: source.Bool(true)},
		{Name: "pull.number", Value: source.Int(12)},
		{Name: "pull.state", Value: source.String("OPEN")},
		{Name: "pull.labels", Value: source.List("bug", "p1")},
		{Name: "pull.window", Value: source.Duration(90 * time.Minute)},
		{Name: "pull.opened_at", Value: source.Time(t0)},
		{Name: "pull.waiting", Value: source.Since(t0)},
		{Name: "pull.first.due", Value: source.At(t0.Add(2 * time.Hour))},
		{Name: "pull.stalled", Value: source.After(t0, 48*time.Hour, source.WorkingDays)},
		{Name: "pull.quiet", Value: source.After(t0, 6*time.Hour, source.Calendar)},
	}
	st := source.RoundState{
		Marks: map[string]string{"reviewed": "abc123"}, SentAt: t0, AgentHome: "home-a", FirstRoundSkipped: true, Today: 1, DayTotal: 4, Paused: true,
		Pending: []source.PendingRound{{Intent: "review", Reason: "held", Since: t0, Deadline: t0.Add(time.Hour)}},
	}
	p := planner{Stub: sourcetest.NewStub(), facts: facts, got: make(chan source.RoundState, 1)}
	c := sourcetest.Launch(t, "pulls", p, (&recorder{}).host())
	plan, err := c.Plan(t.Context(), pr, st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, source.Plan{Facts: facts, Due: t0.Add(time.Hour), Rank: 3}) {
		t.Errorf("plan\n%+v\nwant facts\n%+v", plan, facts)
	}
	if got := <-p.got; !reflect.DeepEqual(got, st) {
		t.Errorf("the planner got round state %+v, want %+v", got, st)
	}
	for _, f := range plan.Facts {
		temporal := slices.Contains([]string{"pull.waiting", "pull.first.due", "pull.stalled", "pull.quiet"}, f.Name)
		if f.Value.Temporal() != temporal {
			t.Errorf("%s: Temporal = %v", f.Name, f.Value.Temporal())
		}
	}
}

func TestEveryCapabilityHasAtMostFiveMethods(t *testing.T) {
	capabilities := []reflect.Type{
		reflect.TypeFor[source.Ingester](), reflect.TypeFor[source.Presenter](), reflect.TypeFor[source.Publisher](),
		reflect.TypeFor[source.Lander](), reflect.TypeFor[source.Briefer](), reflect.TypeFor[source.Planner](),
		reflect.TypeFor[source.Tooler](), reflect.TypeFor[source.CLIRunner](),
	}
	brokers := []reflect.Type{
		reflect.TypeFor[source.Reporter](), reflect.TypeFor[source.Ledger](), reflect.TypeFor[source.Reader](),
		reflect.TypeFor[source.Gates](), reflect.TypeFor[source.Recorder](), reflect.TypeFor[source.Wakes](),
		reflect.TypeFor[source.Services](), reflect.TypeFor[source.Source](),
	}
	for _, i := range append(slices.Clone(capabilities), brokers...) {
		if n := i.NumMethod(); n > 5 {
			t.Errorf("%s has %d methods", i.Name(), n)
		}
	}
	union := func(ts []reflect.Type) []string {
		var out []string
		for _, i := range ts {
			for m := range i.Methods() {
				out = append(out, m.Name)
			}
		}
		slices.Sort(out)
		return out
	}
	rpcs := func(svc reflect.Type, skip ...string) []string {
		var out []string
		for m := range svc.Methods() {
			if !strings.HasPrefix(m.Name, "mustEmbed") && !slices.Contains(skip, m.Name) {
				out = append(out, m.Name)
			}
		}
		slices.Sort(out)
		return out
	}
	if got, want := rpcs(reflect.TypeFor[pb.SourceServiceServer](), "Capabilities"), union(capabilities); !slices.Equal(got, want) {
		t.Errorf("SourceService is\n%v\nnot the union of the capabilities\n%v", got, want)
	}
	if got, want := rpcs(reflect.TypeFor[pb.HostServiceServer]()), union(brokers[:len(brokers)-1]); !slices.Equal(got, want) {
		t.Errorf("HostService is\n%v\nnot the union of the broker interfaces\n%v", got, want)
	}
	if n := len(source.Capabilities(everything{})); n != len(capabilities) {
		t.Errorf("Capabilities names %d of %d capabilities", n, len(capabilities))
	}
}

// everything implements every capability, to count Capabilities' names.
type everything struct {
	source.Source
	source.Ingester
	source.Presenter
	source.Publisher
	source.Lander
	source.Briefer
	source.Planner
	source.Tooler
	source.CLIRunner
}
