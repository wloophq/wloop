package omnibox

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// KindCall is the loop event of one omnibox agent call, recorded on the empty unit; its Data is the cost
// in USD.
const KindCall = "omnibox_call"

// ErrNoText is a request with nothing to ask.
var ErrNoText = errors.New("the request is empty")

// Why codes of the operations Ask drops.
const (
	DropUnknownOperation    = "unknown_operation"
	DropIncompleteOperation = "incomplete_operation"
	DropUnknownTask         = "unknown_task"
	DropUnknownUnit         = "unknown_unit"
	DropOutsideVocabulary   = "outside_vocabulary"
)

// Asker runs the omnibox's read-only agent call: one headless call that reads a snapshot of the read model
// and answers a list or a proposal. It stores nothing but the call's cost and resolves no gate.
type Asker struct {
	Agent turns.Asker
	// Budget books the call against the headless caps, shared with the other calls that are not turns.
	Budget *turns.Budget
	// Record stores the call's cost as a KindCall event, so the caps count it; nil stores none.
	Record func(ctx context.Context, cost float64) error
	// Allowed is why the omnibox's vocabulary refuses op, "" when it runs it; nil refuses every operation.
	Allowed func(op api.OmniboxOperation) (why string, params map[string]string)
	// Repos are the repositories the user works in; nil lists none.
	Repos func(ctx context.Context) ([]string, error)
	// Wrapper is the profile's prompt around the request, read at each call.
	Wrapper func() (Wrapper, error)
	// Dir holds each call's snapshot directory while the call runs.
	Dir string
	// Tier is the model tier of the call; "" is balanced.
	Tier turns.Tier
}

// Ask runs one call on in over snap and returns its answer, its task ids and operations checked against
// snap and the vocabulary. It passes turns.ErrOverCap, turns.ErrNoRestricted and the agent's errors through.
func (a *Asker) Ask(ctx context.Context, in api.OmniboxText, snap Snapshot) (api.OmniboxAnswer, error) {
	if strings.TrimSpace(in.Text) == "" {
		return api.OmniboxAnswer{}, ErrNoText
	}
	if a.Repos != nil {
		repos, err := a.Repos(ctx)
		if err != nil {
			return api.OmniboxAnswer{}, fmt.Errorf("listing the repositories: %w", err)
		}
		snap.Repos = repos
	}
	dir, err := snap.write(a.Dir)
	if dir != "" {
		defer os.RemoveAll(dir)
	}
	if err != nil {
		return api.OmniboxAnswer{}, fmt.Errorf("writing the omnibox snapshot: %w", err)
	}
	q, err := a.question(in, dir)
	if err != nil {
		return api.OmniboxAnswer{}, err
	}
	if rd, ok := a.Agent.(headless.Readier); ok {
		if err := rd.Ready(ctx, q); err != nil {
			return api.OmniboxAnswer{}, err
		}
	}
	if err := a.Budget.Reserve(ctx); err != nil {
		return api.OmniboxAnswer{}, err
	}
	got, callErr := a.Agent.Ask(ctx, q)
	if err := a.Budget.Settle(ctx, got.CostUSD, a.Record); err != nil {
		callErr = errors.Join(callErr, fmt.Errorf("recording the omnibox call's cost: %w", err))
	}
	if callErr != nil {
		return api.OmniboxAnswer{}, callErr
	}
	out, err := a.check(got.JSON, snap)
	out.CostUSD = got.CostUSD
	return out, err
}

func (a *Asker) question(in api.OmniboxText, dir string) (turns.Ask, error) {
	if a.Wrapper == nil {
		return turns.Ask{}, errors.New("no omnibox wrapper is wired")
	}
	w, err := a.Wrapper()
	if err != nil {
		return turns.Ask{}, fmt.Errorf("reading the omnibox wrapper: %w", err)
	}
	prompt, err := w.Prompt(in, dir)
	if err != nil {
		return turns.Ask{}, err
	}
	tier := a.Tier
	if tier == "" {
		tier = turns.TierBalanced
	}
	return turns.Ask{Tier: tier, Prompt: prompt, Schema: Schema(), MaxCostUSD: a.Budget.Caps.PerCallUSD,
		Tools: slices.Clone(turns.ReadOnlyTools), Dirs: []string{dir}}, nil
}

// answer is the call's JSON as the schema shapes it.
type answer struct {
	Answer *struct {
		Text  string   `json:"text"`
		Tasks []string `json:"tasks"`
	} `json:"answer"`
	Proposal *api.OmniboxProposal `json:"proposal"`
}

// check decodes the call's answer and keeps what snap and the vocabulary allow.
func (a *Asker) check(raw []byte, snap Snapshot) (api.OmniboxAnswer, error) {
	var got answer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		return api.OmniboxAnswer{}, fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	if (got.Answer == nil) == (got.Proposal == nil) {
		return api.OmniboxAnswer{}, fmt.Errorf("%w: the answer holds neither or both of answer and proposal", turns.ErrUnfit)
	}
	tasks := map[string]api.TaskView{}
	units := map[unit.Unit]bool{}
	for _, t := range snap.Tasks {
		tasks[t.ID] = t
		for _, u := range t.Units {
			units[u] = true
		}
	}
	if got.Answer != nil {
		list := &api.OmniboxList{Text: got.Answer.Text, Tasks: []api.TaskView{}}
		for _, id := range got.Answer.Tasks {
			if t, ok := tasks[id]; ok {
				list.Tasks = append(list.Tasks, t)
			}
		}
		return api.OmniboxAnswer{Answer: list}, nil
	}
	if len(got.Proposal.Operations) > MaxOperations {
		return api.OmniboxAnswer{}, fmt.Errorf("%w: %d operations, over %d", turns.ErrUnfit, len(got.Proposal.Operations), MaxOperations)
	}
	out := api.OmniboxAnswer{Proposal: &api.OmniboxProposal{Summary: got.Proposal.Summary, Question: got.Proposal.Question,
		Operations: []api.OmniboxOperation{}}}
	for _, op := range got.Proposal.Operations {
		why, params := a.refusal(op, tasks, units)
		if why != "" {
			out.Dropped = append(out.Dropped, api.OmniboxDropped{Operation: op, Why: why, Params: params})
			continue
		}
		out.Proposal.Operations = append(out.Proposal.Operations, op)
	}
	return out, nil
}

// refusal is why op is dropped, "" when it is kept.
func (a *Asker) refusal(op api.OmniboxOperation, tasks map[string]api.TaskView, units map[unit.Unit]bool) (string, map[string]string) {
	switch op.Op {
	case "action":
		if op.Unit == nil || op.Action == "" {
			return DropIncompleteOperation, map[string]string{"op": op.Op}
		}
		if !units[*op.Unit] {
			return DropUnknownUnit, map[string]string{"unit": op.Unit.Label()}
		}
	case "verb":
		if op.Task == "" || op.Verb == "" {
			return DropIncompleteOperation, map[string]string{"op": op.Op}
		}
		if _, ok := tasks[op.Task]; !ok {
			return DropUnknownTask, map[string]string{"task": op.Task}
		}
	case "capture":
		if strings.TrimSpace(op.Title) == "" {
			return DropIncompleteOperation, map[string]string{"op": op.Op}
		}
	case "start":
		if strings.TrimSpace(op.Brief) == "" {
			return DropIncompleteOperation, map[string]string{"op": op.Op}
		}
	default:
		return DropUnknownOperation, map[string]string{"op": op.Op}
	}
	if a.Allowed == nil {
		return DropOutsideVocabulary, map[string]string{"name": cmp.Or(op.Action, op.Verb, op.Op)}
	}
	return a.Allowed(op)
}
