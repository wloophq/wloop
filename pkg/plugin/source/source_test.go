package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	gates     []string
}

func (r *recorder) note(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates = append(r.gates, fmt.Sprintf(format, args...))
}

func (r *recorder) OpenGate(_ context.Context, req source.GateRequest) (source.GateRef, error) {
	r.note("open %s %s %s", req.Session, req.Draft.Kind, req.Draft.Text)
	return source.GateRef{Gate: "g-1", Draft: 4, State: "approved"}, nil
}

func (r *recorder) RecordSent(_ context.Context, d source.Draft, session, tool string) (source.GateRef, error) {
	r.note("sent %s %s %s", session, tool, d.Text)
	return source.GateRef{Gate: "g-2", Draft: 5}, nil
}

func (r *recorder) RehomeDrafts(_ context.Context, moves []source.UnitMove) (int, error) {
	r.note("rehome %s->%s", moves[0].From.Key, moves[0].To.Key)
	return len(moves), nil
}

func (r *recorder) SetOverride(_ context.Context, u plugin.Unit, set, ifMatch map[string]string) (bool, error) {
	r.note("override %s set %v if %v", u.Key, set, ifMatch)
	return ifMatch["muted_kind"] == "", nil
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
	return source.AskResult{Text: "yes", Model: "small-1", CostUSD: ptr(0.02), Duration: 2 * time.Second, Tokens: 120}, nil
}

func (r *recorder) Meter(context.Context, source.Sample) error { return nil }

func (r *recorder) Config(context.Context) (source.Config, error) {
	return source.Config{Plugin: json.RawMessage(`{"enabled":true}`), Host: json.RawMessage(`{"sweep":{"fast":"1m"}}`)}, nil
}

func (r *recorder) Profile(_ context.Context, path string) ([]byte, error) {
	return []byte("profile " + path), nil
}

func (r *recorder) Events(_ context.Context, q source.EventQuery) ([]source.Event, error) {
	return []source.Event{{Unit: q.Units[0], Kind: q.Kinds[0], Actor: "pulls:ana", At: q.Since, Key: "review:9"}}, nil
}

type sessions struct{ rec *recorder }

func (s sessions) Tasks(context.Context, []plugin.Unit) ([]source.Task, error) { return nil, nil }
func (s sessions) Books(context.Context, []plugin.Unit) ([]source.Book, error) { return nil, nil }
func (s sessions) RoundState(context.Context, plugin.Unit) (source.RoundState, error) {
	return source.RoundState{}, nil
}
func (s sessions) Workspaces(context.Context, string) (source.Listing, error) {
	return source.Listing{}, nil
}

func (s sessions) Session(_ context.Context, handle string) (source.Session, error) {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.sessions = append(s.rec.sessions, handle)
	return source.Session{Unit: pr, Turn: "t-1", TurnSeq: 7, Intent: "review", AllowedEffects: []string{"pull.nudged"}}, nil
}

func (r *recorder) host() source.Host {
	return source.Host{Reporter: r, Ledger: r, Reader: sessions{r}, Gates: r, Services: r, History: r}
}

func snapshot() source.Snapshot {
	anchor := source.Anchor{Unit: pr, Ref: "review:9", Permalink: "https://pulls.example/acme/api/pull/12#r9", At: t0}
	return source.Snapshot{
		Unit: pr, Rev: 42, At: t0,
		Data:      json.RawMessage(`{"title":"Add retries","draft":false}`),
		Situation: &source.Situation{Kind: "to_review", WaitingOn: "pulls:bruno", Since: t0, Reason: "Ana asked", Stalled: source.After(t0, 48*time.Hour, source.WorkingDays)},
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
		Facts:       []source.Fact{{Name: "pull.state", Value: source.String("OPEN")}, {Name: "pull.waiting", Value: source.Since(t0)}},
		Signals:     []source.Signal{{Name: "stalled", On: true, Since: t0}, {Name: "ready"}},
		Owes:        []source.Person{bru},
		Offers:      []source.Offer{{Action: "pull.merge", Args: json.RawMessage(`{"method":"squash"}`)}},
		Extraction:  &source.ExtractionHealth{Pending: 3, BudgetHit: true, Oldest: t0},
		Contexts:    []string{"@repo-api"},
		Due:         t0.Add(48 * time.Hour),
		Links:       []source.Link{{URL: "https://chat.example/archives/C1/p1", Kind: "mentions", Item: "pull:review", Evidence: anchor}},
		Facets:      map[string]string{"repo": "acme/api", "author": "ana"},
		Views:       []string{"needs-me", "all"},
		FacetValues: map[string][]string{"waits-on": {"me", "ana"}},
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
	evs, err := h.Events(ctx, source.EventQuery{Units: []plugin.Unit{se.Unit}, Kinds: []string{"pull.merged"}, Since: t0})
	if err != nil {
		return source.ToolResult{}, err
	}
	cfg, err := h.Config(ctx)
	if err != nil {
		return source.ToolResult{}, err
	}
	prompt, err := h.Profile(ctx, "prompts/extract.md")
	if err != nil {
		return source.ToolResult{}, err
	}
	gate, err := h.OpenGate(ctx, source.GateRequest{Draft: source.Draft{Unit: se.Unit, Kind: "nudge", Text: "Any news?"}, Session: call.Session})
	if err != nil {
		return source.ToolResult{}, err
	}
	if _, err := h.RecordSent(ctx, source.Draft{Unit: se.Unit, Text: "Done"}, call.Session, call.Tool); err != nil {
		return source.ToolResult{}, err
	}
	moved, err := h.RehomeDrafts(ctx, []source.UnitMove{{From: plugin.Unit{Provider: "pulls", Key: "a"}, To: plugin.Unit{Provider: "pulls", Key: "b"}}})
	if err != nil {
		return source.ToolResult{}, err
	}
	written, err := h.SetOverride(ctx, se.Unit, map[string]string{"muted_until": ""}, map[string]string{"muted_kind": "review"})
	if err != nil {
		return source.ToolResult{}, err
	}
	out, _ := json.Marshal(map[string]any{
		"effects": se.AllowedEffects, "answer": ans.Text, "model": ans.Model, "cost": *ans.CostUSD, "item": res[0].Item,
		"priority": source.CallOf(ctx).Priority, "wake": errors.Is(wakeErr, source.ErrUnsupported),
		"event": evs[0].Key, "host": string(cfg.Host), "prompt": string(prompt),
		"gate": gate.Gate, "draft": gate.Draft, "state": gate.State, "moved": moved, "written": written,
	})
	return source.ToolResult{Answer: "read", Fields: out}, nil
}

func (s tooler) RunCLI(_ context.Context, req source.CLIRequest, stdout, stderr io.Writer) (int, error) {
	fmt.Fprintf(stdout, "open %s\n", req.Args[0])
	fmt.Fprintf(stderr, "waiting for %s\n", req.Config.Plugin)
	fmt.Fprint(stdout, "logged in\n")
	return 3, nil
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
	if res.Answer != "read" || res.IsError {
		t.Errorf("answer %q, error %v", res.Answer, res.IsError)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Fields, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"effects": []any{"pull.nudged"}, "answer": "yes", "model": "small-1", "cost": 0.02, "item": "icreate",
		"priority": float64(source.PriorityBackground), "wake": true, "event": "review:9", "host": `{"sweep":{"fast":"1m"}}`, "prompt": "profile prompts/extract.md",
		"gate": "g-1", "draft": float64(4), "state": "approved", "moved": float64(1), "written": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tool result %v, want %v", got, want)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if want := []string{"open s-1 nudge Any news?", "sent s-1 pull_read Done", "rehome a->b", "override acme/api#12 set map[muted_until:] if map[muted_kind:review]"}; !slices.Equal(rec.gates, want) {
		t.Errorf("gates\n%q\nwant\n%q", rec.gates, want)
	}
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
	if !slices.Equal(caps, []source.Capability{source.CapabilityIngester, source.CapabilityTooler, source.CapabilityCLIRunner}) {
		t.Errorf("capabilities %v", caps)
	}
	if _, err := c.Publish(t.Context(), source.Draft{Unit: pr}); !errors.Is(err, source.ErrUnsupported) {
		t.Errorf("Publish on a plugin with no publisher = %v", err)
	}

	var stdout, stderr strings.Builder
	exit, err := c.RunCLI(t.Context(), source.CLIRequest{Args: []string{"login"}, Config: source.Config{Plugin: json.RawMessage(`{"client_id":"c1"}`)}}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if exit != 3 || stdout.String() != "open login\nlogged in\n" || stderr.String() != "waiting for {\"client_id\":\"c1\"}\n" {
		t.Errorf("RunCLI = %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
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
	return source.Plan{Rounds: rounds(p.facts)}, nil
}

func rounds(facts []source.Fact) []source.Round {
	return []source.Round{{
		Name: "address", Workspace: source.WorkspaceRef{Manager: "trees", ID: "w1"}, Head: "abc123", Reason: "checks failed",
		Observed: true, TurnFacts: facts, OnSent: map[string]string{"addressed.checks": "abc123"}, OnSkipped: map[string]string{"skipped": "1"}, NotDue: "a check is pending",
	}}
}

func (planner) Payload(context.Context, source.PayloadRequest) (source.PayloadResult, error) {
	return source.PayloadResult{}, nil
}
func (planner) ApplyEffect(context.Context, source.EffectRequest) error { return nil }
func (planner) Proposals(context.Context, time.Time) ([]source.Draft, error) {
	return nil, nil
}
func (planner) Placement(context.Context, plugin.Unit, string) (source.Placements, error) {
	return source.Placements{}, nil
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
		{Name: "pull.never_waited", Value: source.Since(time.Time{})},
		{Name: "pull.no_deadline", Value: source.At(time.Time{})},
		{Name: "pull.never_stalled", Value: source.After(time.Time{}, time.Hour, source.WorkingDays)},
		{Name: "pull.closed_at", Value: source.Time(time.Time{})},
	}
	st := source.RoundState{
		Marks: map[string]string{"reviewed": "abc123"}, SentAt: t0, AgentHome: "home-a", Today: 1, DayTotal: 4, Paused: true,
		Pending:   []source.PendingRound{{Intent: "review", Reason: "held", Since: t0, Deadline: t0.Add(time.Hour)}},
		Overrides: map[string]string{"next_guidance": "check the retries"},
		Blocked:   &source.Blocked{Question: "Which branch?", Root: "/w/api", Folder: "api", Since: t0},
	}
	p := planner{Stub: sourcetest.NewStub(), facts: facts, got: make(chan source.RoundState, 1)}
	c := sourcetest.Launch(t, "pulls", p, (&recorder{}).host())
	plan, err := c.Plan(t.Context(), pr, st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, source.Plan{Rounds: rounds(facts)}) {
		t.Errorf("plan\n%+v\nwant facts\n%+v", plan, facts)
	}
	if got := <-p.got; !reflect.DeepEqual(got, st) {
		t.Errorf("the planner got round state %+v, want %+v", got, st)
	}
	for _, f := range plan.Rounds[0].TurnFacts {
		temporal := slices.Contains([]string{"pull.waiting", "pull.first.due", "pull.stalled", "pull.quiet", "pull.never_waited", "pull.no_deadline", "pull.never_stalled"}, f.Name)
		if f.Value.Temporal() != temporal {
			t.Errorf("%s: Temporal = %v", f.Name, f.Value.Temporal())
		}
		zero := strings.HasPrefix(f.Name, "pull.never") || f.Name == "pull.no_deadline" || f.Name == "pull.closed_at"
		if zero != f.Value.Time.IsZero() && (f.Value.Temporal() || f.Value.Kind == source.KindTime) {
			t.Errorf("%s: a zero time did not stay zero: %v", f.Name, f.Value.Time)
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
		reflect.TypeFor[source.Services](), reflect.TypeFor[source.History](), reflect.TypeFor[source.Source](),
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
