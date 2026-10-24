package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// classifyStore keeps classify outputs by fingerprint and the day's calls.
type classifyStore interface {
	Output(ctx context.Context, u unit.Unit, workflow, fingerprint string) (json.RawMessage, bool, error)
	Save(ctx context.Context, u unit.Unit, workflow, fingerprint string, output json.RawMessage, at time.Time) error
	Spend(ctx context.Context, at time.Time, limit int) (bool, error)
	Spent(ctx context.Context, at time.Time) (int, error)
}

// classifyDeps are classify's store, the agent's headless calls and the
// brief breaker; a nil Store or Asker refuses every classify.
type classifyDeps struct {
	Store       classifyStore
	Asker       turns.Asker
	BreakerOpen func() bool
}

// classifier runs classify calls on background workers, never two for one
// workflow, unit and fingerprint.
type classifier struct {
	s        *stepSet
	mu       sync.Mutex
	inflight map[string]bool
	// failed is the last call's failure by key, which the next run returns
	// as a failed attempt instead of spending another call.
	failed map[string]error
	wg     sync.WaitGroup
}

// wait waits for the calls in flight.
func (c *classifier) wait() { c.wg.Wait() }

func (c *classifier) deps() classifyDeps { return c.s.d.Classify }

// fingerprint is the unit's fingerprint fact, which keys its output.
func fingerprint(u unit.Unit, f workflow.Facts) string {
	slot, ok := builtin.Slots[u.Provider]
	if !ok {
		slot = string(u.Provider)
	}
	return factString(f, slot+".fingerprint")
}

// cached is the output stored for the unit's current fingerprint.
func (c *classifier) cached(ctx context.Context, sc stepCall) (json.RawMessage, string, bool, error) {
	f, err := c.s.facts(ctx, sc.u)
	if err != nil {
		return nil, "", false, err
	}
	fp := fingerprint(sc.u, f)
	store := c.deps().Store
	if store == nil {
		return nil, fp, false, nil
	}
	out, ok, err := store.Output(ctx, sc.u, sc.why.Workflow, fp)
	return out, fp, ok, err
}

// refusal is why no call may start now: no store or agent, the day's cap
// spent, or the brief breaker open.
func (c *classifier) refusal(ctx context.Context) (string, int, error) {
	d := c.deps()
	if d.Store == nil || d.Asker == nil {
		return "no agent answers headless calls", 0, nil
	}
	cfg, err := c.s.e.loadConfig()
	if err != nil {
		return "", 0, err
	}
	limit := cfg.Classify.DailyCap
	spent, err := d.Store.Spent(ctx, c.s.e.Now())
	if err != nil {
		return "", 0, err
	}
	if spent >= limit {
		return capReached(limit), limit, nil
	}
	if d.BreakerOpen != nil && d.BreakerOpen() {
		return "the brief breaker is open", limit, nil
	}
	return "", limit, nil
}

func capReached(limit int) string { return fmt.Sprintf("daily classify cap %d reached", limit) }

func (s *stepSet) classifyCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	_, _, ok, err := s.classify.cached(ctx, c)
	if err != nil || ok {
		return "", err
	}
	if s.classify.busy(ctx, c) {
		return "", nil
	}
	why, _, err := s.classify.refusal(ctx)
	return why, err
}

func classifyKey(c stepCall, fp string) string {
	return c.why.Workflow + "\n" + c.u.String() + "\n" + fp
}

func (c *classifier) busy(ctx context.Context, sc stepCall) bool {
	f, err := c.s.facts(ctx, sc.u)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight[classifyKey(sc, fingerprint(sc.u, f))]
}

// classifyRun returns the output stored for the unit's fingerprint, or
// starts the call on a worker, spending one of the day's calls, and
// returns Deferred: the item stays pending until a later run finds it.
func (s *stepSet) classifyRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	out, fp, ok, err := s.classify.cached(ctx, c)
	if err != nil {
		return "", err
	}
	if ok {
		return string(out), nil
	}
	if err := s.classify.takeFailure(c, fp); err != nil {
		return "", err
	}
	return "", s.classify.start(ctx, c, fp)
}

func (c *classifier) takeFailure(sc stepCall, fp string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := classifyKey(sc, fp)
	err := c.failed[key]
	delete(c.failed, key)
	return err
}

func (c *classifier) start(ctx context.Context, sc stepCall, fp string) error {
	key := classifyKey(sc, fp)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight[key] {
		return automation.Deferred{Reason: "classifying"}
	}
	why, limit, err := c.refusal(ctx)
	if err != nil {
		return err
	}
	if why != "" {
		return automation.Refused{Reason: why}
	}
	d := c.deps()
	spent, err := d.Store.Spend(ctx, c.s.e.Now(), limit)
	if err != nil {
		return err
	}
	if !spent {
		return automation.Refused{Reason: capReached(limit)}
	}
	facts, err := c.s.facts(ctx, sc.u)
	if err != nil {
		return err
	}
	prompt, err := classifyPrompt(sc, facts)
	if err != nil {
		return err
	}
	if c.inflight == nil {
		c.inflight = map[string]bool{}
	}
	c.inflight[key] = true
	bg := context.WithoutCancel(ctx)
	c.wg.Go(func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
		}()
		ans, err := d.Asker.Ask(bg, turns.Ask{Tier: turns.TierFast, Prompt: prompt, Schema: sc.arg("schema")})
		if err == nil {
			err = d.Store.Save(bg, sc.u, sc.why.Workflow, fp, ans.JSON, c.s.e.Now())
		}
		if err != nil {
			c.s.logf("%s: classifying %s: %v", sc.why.Workflow, sc.u, err)
			c.mu.Lock()
			if c.failed == nil {
				c.failed = map[string]error{}
			}
			c.failed[key] = fmt.Errorf("classifying failed: %w", err)
			c.mu.Unlock()
		}
	})
	return automation.Deferred{Reason: "classifying"}
}

func classifyPrompt(c stepCall, facts workflow.Facts) (string, error) {
	text := c.arg("prompt")
	if text == "" {
		return "", errors.New("classify has no prompt")
	}
	raw, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return "", err
	}
	return text + "\n\nThe facts of " + c.u.Label() + ", as JSON:\n\n" + string(raw), nil
}

// classifiedUnits adds to a unit's facts the outputs classify stored for
// its fingerprint, as classify.<workflow>.<field>.
type classifiedUnits struct {
	automation.Units
	c         *classifier
	workflows func() []workflow.Workflow
}

func (u classifiedUnits) Facts(ctx context.Context, un unit.Unit) (workflow.Facts, error) {
	f, err := u.Units.Facts(ctx, un)
	if err != nil {
		return f, err
	}
	return u.add(ctx, un, f)
}

func (u classifiedUnits) add(ctx context.Context, un unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	store := u.c.deps().Store
	if store == nil || u.workflows == nil {
		return f, nil
	}
	out, cloned := f, false
	for _, w := range u.workflows() {
		if w.Provider != un.Provider || !slices.ContainsFunc(w.Steps, func(st workflow.Step) bool { return st.Use == stepClassify }) {
			continue
		}
		raw, ok, err := store.Output(ctx, un, w.Name, factString(f, w.Slot+".fingerprint"))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var fields map[string]any
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		if !cloned {
			out, cloned = maps.Clone(f), true
		}
		for k, v := range fields {
			out["classify."+w.Name+"."+k] = factValue(v)
		}
	}
	return out, nil
}

// factValue is a JSON value as a fact: a whole number an int, a list of
// strings a []string, anything else not a string or bool its JSON text.
func factValue(v any) any {
	switch x := v.(type) {
	case string, bool:
		return x
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int(x)
		}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				raw, _ := json.Marshal(v)
				return string(raw)
			}
			out = append(out, s)
		}
		return out
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
