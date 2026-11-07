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

// AskRequest is what the user typed and the questions the agent asked before, with the user's answers.
type AskRequest struct {
	Text   string
	Follow []AskFollowUp
}

// AskFollowUp is a question the agent asked and the user's answer.
type AskFollowUp struct{ Question, Answer string }

// AskOp is one operation the agent proposes, shaped as the answer schema shapes it: Op "action" (Action on
// Unit), "verb" (Verb on Task), "capture" (a task titled Title) or "start" (a task from Brief, started in Repo).
type AskOp struct {
	Op       string     `json:"op"`
	Unit     *unit.Unit `json:"unit,omitempty"`
	Task     string     `json:"task,omitempty"`
	Action   string     `json:"action,omitempty"`
	Verb     string     `json:"verb,omitempty"`
	Title    string     `json:"title,omitempty"`
	Brief    string     `json:"brief,omitempty"`
	Repo     string     `json:"repo,omitempty"`
	Guidance string     `json:"guidance,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}

// AskReply is the call's answer, a list or a proposal, with the proposed operations dropped and the call's
// cost, nil when the agent reported none.
type AskReply struct {
	List     *AskList
	Proposal *AskProposal
	Dropped  []AskDropped
	CostUSD  *float64
}

// AskList is a question's answer: a text and the ids of the snapshot's tasks it lists.
type AskList struct {
	Text  string   `json:"text"`
	Tasks []string `json:"tasks"`
}

// AskProposal is operations to confirm and the one question the agent asks first, if any.
type AskProposal struct {
	Summary    string  `json:"summary"`
	Question   string  `json:"question,omitempty"`
	Operations []AskOp `json:"operations"`
}

// AskDropped is a proposed operation dropped: Why is its code, Params the values its words name.
type AskDropped struct {
	Op     AskOp
	Why    string
	Params map[string]string
}

// Asker runs the omnibox's read-only agent call: one headless call that reads a snapshot of the read model
// and answers a list or a proposal. It stores nothing but the call's cost and resolves no gate.
type Asker struct {
	Agent turns.Asker
	// Budget books the call against the headless caps, shared with the other calls that are not turns.
	Budget *turns.Budget
	// Record stores the call's cost as a KindCall event, so the caps count it; nil stores none.
	Record func(ctx context.Context, cost float64) error
	// Allowed is why the omnibox's vocabulary refuses op, "" when it runs it; nil refuses every operation.
	Allowed func(op AskOp) (why string, params map[string]string)
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
func (a *Asker) Ask(ctx context.Context, in AskRequest, snap Snapshot) (AskReply, error) {
	if strings.TrimSpace(in.Text) == "" {
		return AskReply{}, ErrNoText
	}
	if a.Repos != nil {
		repos, err := a.Repos(ctx)
		if err != nil {
			return AskReply{}, fmt.Errorf("listing the repositories: %w", err)
		}
		snap.Repos = repos
	}
	dir, err := snap.write(a.Dir)
	if dir != "" {
		defer os.RemoveAll(dir)
	}
	if err != nil {
		return AskReply{}, fmt.Errorf("writing the omnibox snapshot: %w", err)
	}
	q, err := a.question(in, dir)
	if err != nil {
		return AskReply{}, err
	}
	if rd, ok := a.Agent.(headless.Readier); ok {
		if err := rd.Ready(ctx, q); err != nil {
			return AskReply{}, err
		}
	}
	if err := a.Budget.Reserve(ctx); err != nil {
		return AskReply{}, err
	}
	got, callErr := a.Agent.Ask(ctx, q)
	if err := a.Budget.Settle(ctx, got.CostUSD, a.Record); err != nil {
		callErr = errors.Join(callErr, fmt.Errorf("recording the omnibox call's cost: %w", err))
	}
	if callErr != nil {
		return AskReply{}, callErr
	}
	out, err := a.check(got.JSON, snap)
	out.CostUSD = got.CostUSD
	return out, err
}

func (a *Asker) question(in AskRequest, dir string) (turns.Ask, error) {
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
	Answer   *AskList     `json:"answer"`
	Proposal *AskProposal `json:"proposal"`
}

// check decodes the call's answer and keeps what snap and the vocabulary allow.
func (a *Asker) check(raw []byte, snap Snapshot) (AskReply, error) {
	var got answer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		return AskReply{}, fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	if (got.Answer == nil) == (got.Proposal == nil) {
		return AskReply{}, fmt.Errorf("%w: the answer holds neither or both of answer and proposal", turns.ErrUnfit)
	}
	tasks := map[string]bool{}
	units := map[unit.Unit]bool{}
	for _, t := range snap.Tasks {
		tasks[t.ID] = true
		for _, u := range t.Units {
			units[u] = true
		}
	}
	if got.Answer != nil {
		list := &AskList{Text: got.Answer.Text, Tasks: []string{}}
		for _, id := range got.Answer.Tasks {
			if tasks[id] {
				list.Tasks = append(list.Tasks, id)
			}
		}
		return AskReply{List: list}, nil
	}
	if len(got.Proposal.Operations) > MaxOperations {
		return AskReply{}, fmt.Errorf("%w: %d operations, over %d", turns.ErrUnfit, len(got.Proposal.Operations), MaxOperations)
	}
	out := AskReply{Proposal: &AskProposal{Summary: got.Proposal.Summary, Question: got.Proposal.Question, Operations: []AskOp{}}}
	for _, op := range got.Proposal.Operations {
		if why, params := a.refusal(op, tasks, units); why != "" {
			out.Dropped = append(out.Dropped, AskDropped{Op: op, Why: why, Params: params})
			continue
		}
		out.Proposal.Operations = append(out.Proposal.Operations, op)
	}
	return out, nil
}

// refusal is why op is dropped, "" when it is kept.
func (a *Asker) refusal(op AskOp, tasks map[string]bool, units map[unit.Unit]bool) (string, map[string]string) {
	incomplete := map[string]string{"op": op.Op}
	switch op.Op {
	case "action":
		if op.Unit == nil || op.Action == "" {
			return DropIncompleteOperation, incomplete
		}
		if !units[*op.Unit] {
			return DropUnknownUnit, map[string]string{"unit": op.Unit.Label()}
		}
	case "verb":
		if op.Task == "" || op.Verb == "" {
			return DropIncompleteOperation, incomplete
		}
		if !tasks[op.Task] {
			return DropUnknownTask, map[string]string{"id": op.Task}
		}
	case "capture":
		if strings.TrimSpace(op.Title) == "" {
			return DropIncompleteOperation, incomplete
		}
	case "start":
		if strings.TrimSpace(op.Brief) == "" {
			return DropIncompleteOperation, incomplete
		}
	default:
		return DropUnknownOperation, incomplete
	}
	if a.Allowed == nil {
		return DropOutsideVocabulary, map[string]string{"name": cmp.Or(op.Action, op.Verb, op.Op)}
	}
	return a.Allowed(op)
}
