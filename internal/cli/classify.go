package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/task"
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

// classified is what keys one classify call: the fingerprint its output is stored by, and the tasks its with
// lists, in the list's order.
type classified struct {
	fp   string
	with []task.ID
}

// empty says the call lists tasks with with, and none.
func (k classified) empty(sc stepCall) bool { return sc.arg(withArg) != "" && len(k.with) == 0 }

// cached is the output stored for the unit's current fingerprint.
func (c *classifier) cached(ctx context.Context, sc stepCall) (json.RawMessage, classified, bool, error) {
	f, err := c.s.facts(ctx, sc.u)
	if err != nil {
		return nil, classified{}, false, err
	}
	k, err := c.key(ctx, sc.arg(withArg), sc.u, f)
	if err != nil || k.empty(sc) {
		return nil, k, false, err
	}
	store := c.deps().Store
	if store == nil {
		return nil, k, false, nil
	}
	out, ok, err := store.Output(ctx, sc.u, sc.why.Workflow, k.fp)
	return out, k, ok, err
}

// withArg is classify's argument naming a list fact of task ids.
const withArg = "with"

// The most of a listed task the prompt reads.
const (
	withItems      = 5
	withNotesRunes = 1000
)

// readingWith marks a context whose facts reads are those a with key takes, which add no with output.
type readingWith struct{}

// key is u's fingerprint; with a with fact, what the judgment reads instead: the contents of u's task, else u's
// fingerprint, and of the tasks the fact lists, so a write that changes none of them, such as opening a task,
// keeps the output.
func (c *classifier) key(ctx context.Context, with string, u unit.Unit, f workflow.Facts) (classified, error) {
	k := classified{fp: fingerprint(u, f)}
	if with == "" {
		return k, nil
	}
	inner := context.WithValue(workflow.WithReads(ctx, with), readingWith{}, true)
	v, ok := f[with]
	if _, unrequested := v.(workflow.Unrequested); unrequested || !ok {
		g, err := c.s.facts(inner, u)
		if err != nil {
			return k, err
		}
		v = g[with]
	}
	ids, ok := v.([]string)
	if !ok && v != nil {
		return k, fmt.Errorf("classify: with %s is %v, not a list of task ids", with, v)
	}
	self, listed, err := c.promptTasks(inner, promptMarks{}, u, ids)
	if err != nil {
		return k, err
	}
	parts := make([]string, 0, len(listed))
	for _, t := range listed {
		k.with = append(k.with, task.ID(t.ID))
		parts = append(parts, contentKey(t))
	}
	slices.Sort(parts)
	if self != nil {
		parts = append([]string{contentKey(*self)}, parts...)
	} else {
		parts = append([]string{k.fp}, parts...)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	k.fp = "with:" + hex.EncodeToString(sum[:16])
	return k, nil
}

func contentKey(t promptTask) string {
	return strings.Join(append([]string{t.ID, t.Title, t.Notes}, t.Items...), "\x1f")
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
	limit := cfg.Classify.PerDay
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
	_, k, ok, err := s.classify.cached(ctx, c)
	if err != nil || ok || k.empty(c) {
		return "", err
	}
	if s.classify.busy(c, k.fp) {
		return "", nil
	}
	why, _, err := s.classify.refusal(ctx)
	return why, err
}

func classifyKey(c stepCall, fp string) string {
	return c.why.Workflow + "\n" + c.u.String() + "\n" + fp
}

func (c *classifier) busy(sc stepCall, fp string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight[classifyKey(sc, fp)]
}

// classifyRun returns the output stored for the unit's fingerprint, or
// starts the call on a worker, spending one of the day's calls, and
// returns Deferred: the item stays pending until a later run finds it.
func (s *stepSet) classifyRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	out, k, ok, err := s.classify.cached(ctx, c)
	if err != nil || k.empty(c) {
		return "", err
	}
	if ok {
		s.classify.told(c, k.fp)
		return string(out), nil
	}
	if err := s.classify.takeFailure(c, k.fp); err != nil {
		return "", err
	}
	return "", s.classify.start(ctx, c, k)
}

func (c *classifier) takeFailure(sc stepCall, fp string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := classifyKey(sc, fp)
	err := c.failed[key]
	delete(c.failed, key)
	return err
}

func (c *classifier) start(ctx context.Context, sc stepCall, k classified) error {
	fp := k.fp
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
	text, err := c.s.file(sc, "prompt")
	if err != nil {
		return err
	}
	schema, err := c.s.file(sc, "schema")
	if err != nil {
		return err
	}
	var prompt string
	if sc.arg(withArg) == "" {
		prompt, err = classifyPrompt(text, sc, facts)
	} else {
		prompt, err = c.withPrompt(ctx, text, sc.u, facts, k.with)
	}
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
		ans, err := d.Asker.Ask(bg, turns.Ask{Tier: turns.TierFast, Prompt: prompt, Schema: schema})
		if err == nil {
			err = validAnswer(schema, ans.JSON)
		}
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

// told records that sc ran with the output stored for fp, once per event that planned it, chained after it.
func (c *classifier) told(sc stepCall, fp string) {
	if c.s.d.Pub == nil {
		return
	}
	var chain workflow.Chain
	key := sc.why.Workflow + "\n" + fp
	if ev := sc.why.Event; ev != nil {
		chain = ev.Chain
		key += "\n" + strconv.FormatInt(ev.ID, 10)
	}
	c.s.d.Pub.Publish(port.Event{Unit: sc.u, Kind: builtin.EventClassified, Key: key,
		Actor: string(task.ActorWorkflow) + ":" + sc.why.Workflow, Data: workflow.ChainData(sc.why.Workflow, chain.Then(sc.why.Workflow)),
		At: c.s.e.Now()})
}

// validAnswer refuses an answer outside the schema that asked for it; the
// loader compiled the schema already.
func validAnswer(schema string, answer []byte) error {
	s, err := jsonschema.Compile([]byte(schema), jsonschema.Options{})
	if err != nil {
		return err
	}
	if err := s.ValidateJSON(answer); err != nil {
		return fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	return nil
}

func classifyPrompt(text string, c stepCall, facts workflow.Facts) (string, error) {
	if strings.TrimSpace(text) == "" {
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
		i := slices.IndexFunc(w.Steps, func(st workflow.Step) bool { return st.Use == stepClassify })
		if w.Provider != un.Provider || i < 0 {
			continue
		}
		fp := factString(f, w.Slot+".fingerprint")
		if with, _ := w.Steps[i].Args[withArg].(string); with != "" {
			if ctx.Value(readingWith{}) != nil || !names(ctx, builtin.ClassifyFact(w.Name, "")) {
				continue
			}
			if _, err := u.c.s.tasks(); err != nil {
				continue
			}
			k, err := u.c.key(ctx, with, un, f)
			if err != nil {
				return nil, err
			}
			if len(k.with) == 0 {
				continue
			}
			fp = k.fp
		}
		raw, ok, err := store.Output(ctx, un, w.Name, fp)
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
			out[builtin.ClassifyFact(w.Name, k)] = factValue(v)
		}
	}
	return out, nil
}

// names says whether a read ctx carries names prefix, as a fact name or inside a text that mentions it.
func names(ctx context.Context, prefix string) bool {
	return slices.ContainsFunc(workflow.ReadsOf(ctx), func(r string) bool { return strings.Contains(r, prefix) })
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
