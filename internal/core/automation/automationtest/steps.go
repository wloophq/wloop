package automationtest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Call is one call a fake step received.
type Call struct {
	Step    workflow.Step
	Unit    unit.Unit
	Why     workflow.Why
	Check   automation.CheckOpts
	Run     automation.RunOpts
	Refusal string
}

type stepUnit struct {
	use string
	u   unit.Unit
}

// Steps runs the steps of a fixed vocabulary in memory: every step passes
// and runs unless a test refuses, fails or defers it.
type Steps struct {
	o     options
	reach map[string]workflow.Reach

	mu        sync.Mutex
	refusals  map[stepUnit]string
	guards    map[string]func(unit.Unit, automation.CheckOpts) string
	checkErrs map[stepUnit]error
	fails     map[stepUnit]error
	defers    map[stepUnit]string
	ran       []Call
	noted     []Call
	dismissed []Call
	checks    []Call
}

// NewSteps knows the steps of reach, by name, with their reach.
func NewSteps(reach map[string]workflow.Reach, opts ...Option) *Steps {
	return &Steps{o: build(opts), reach: maps.Clone(reach), refusals: map[stepUnit]string{},
		guards: map[string]func(unit.Unit, automation.CheckOpts) string{}, checkErrs: map[stepUnit]error{},
		fails: map[stepUnit]error{}, defers: map[stepUnit]string{}}
}

// Step is the step use with its vocabulary reach and args as key, value pairs.
func (f *Steps) Step(use string, args ...string) workflow.Step {
	st := workflow.Step{Use: use, Reach: f.reach[use]}
	for i := 0; i+1 < len(args); i += 2 {
		if st.Args == nil {
			st.Args = map[string]any{}
		}
		st.Args[args[i]] = args[i+1]
	}
	return st
}

// Refuse makes Check and Run of use on u refuse with reason; "" clears it.
func (f *Steps) Refuse(use string, u unit.Unit, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusals[stepUnit{use, u}] = reason
}

// Guard makes Check and Run of use ask fn first; a non-empty answer refuses.
func (f *Steps) Guard(use string, fn func(unit.Unit, automation.CheckOpts) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.guards[use] = fn
}

// CheckErr makes Check of use on u fail with err; nil clears it.
func (f *Steps) CheckErr(use string, u unit.Unit, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkErrs[stepUnit{use, u}] = err
}

// Fail makes Run of use on u return err; nil clears it.
func (f *Steps) Fail(use string, u unit.Unit, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fails[stepUnit{use, u}] = err
}

// Defer makes Run of use on u return Deferred{reason}; "" clears it.
func (f *Steps) Defer(use string, u unit.Unit, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defers[stepUnit{use, u}] = reason
}

func (f *Steps) Ran() []Call       { return f.calls(&f.ran) }
func (f *Steps) Noted() []Call     { return f.calls(&f.noted) }
func (f *Steps) Dismissed() []Call { return f.calls(&f.dismissed) }
func (f *Steps) Checked() []Call   { return f.calls(&f.checks) }

func (f *Steps) calls(p *[]Call) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(*p)
}

func (f *Steps) known(use string) error {
	if _, ok := f.reach[use]; !ok {
		return fmt.Errorf("unknown step %q", use)
	}
	return nil
}

func (f *Steps) refusal(use string, u unit.Unit, o automation.CheckOpts) string {
	if r := f.refusals[stepUnit{use, u}]; r != "" {
		return r
	}
	if g := f.guards[use]; g != nil {
		return g(u, o)
	}
	return ""
}

func (f *Steps) Check(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.CheckOpts) (string, error) {
	f.o.read("steps.check")
	if err := f.known(st.Use); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkErrs[stepUnit{st.Use, u}]; err != nil {
		return "", err
	}
	r := f.refusal(st.Use, u, o)
	f.checks = append(f.checks, Call{Step: st, Unit: u, Why: why, Check: o, Refusal: r})
	return r, nil
}

func (f *Steps) Note(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, refusal string) error {
	f.o.write("steps.note")
	if err := f.known(st.Use); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noted = append(f.noted, Call{Step: st, Unit: u, Why: why, Refusal: refusal})
	return nil
}

func (f *Steps) Run(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.RunOpts) (string, error) {
	f.o.write("steps.run")
	if err := f.known(st.Use); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", automation.Transient{Err: err}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := stepUnit{st.Use, u}
	if r := f.refusal(st.Use, u, automation.CheckOpts{ByHand: o.ByHand}); r != "" {
		return "", automation.Refused{Reason: r}
	}
	if err := f.fails[k]; err != nil {
		return "", err
	}
	if r := f.defers[k]; r != "" {
		return "", automation.Deferred{Reason: r}
	}
	f.ran = append(f.ran, Call{Step: st, Unit: u, Why: why, Run: o})
	return st.Use + " ran", nil
}

func (f *Steps) Dismiss(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why) error {
	f.o.write("steps.dismiss")
	if err := f.known(st.Use); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissed = append(f.dismissed, Call{Step: st, Unit: u, Why: why})
	return nil
}

// Strict makes the fake fail t on any later write.
func (f *Steps) Strict(t testing.TB) { f.o.seal(t) }
