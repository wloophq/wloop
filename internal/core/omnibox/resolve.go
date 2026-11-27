package omnibox

import (
	"context"
	"strings"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// How typed text resolves.
const (
	KindUnit   = "unit"   // a pasted address a plugin claims
	KindVerb   = "verb"   // a known verb's operations, to confirm
	KindSearch = "search" // a search
	KindAgent  = "agent"  // the agent would help: nothing resolves without guessing
)

// Why codes of a resolution that fell to search or to the agent.
const (
	WhyUnclaimed    = "unclaimed_address" // no plugin claims the address; Params["address"]
	WhyNoRef        = "no_unit_or_task"   // a ref names no unit or task; Params["ref"]
	WhyAmbiguous    = "ambiguous_ref"     // a ref names more than one; Params["ref"]
	WhyNothingNamed = "nothing_named"     // a verb names nothing it runs on; Params["verb"]
)

// Ref is what a token names: a unit, with the task that holds it if any, or a task.
type Ref struct {
	Unit *unit.Unit
	Task task.ID
}

// Refs resolve what a person types.
type Refs interface {
	// Find are the units and tasks token names: an address a plugin claims, a unit's label or short label, a
	// task's id or its prefix.
	Find(ctx context.Context, token string) ([]Ref, error)
}

// Resolver classifies typed text; it runs nothing.
type Resolver struct {
	Refs       Refs
	Vocabulary func() Vocabulary
}

// Resolution is what typed text resolves to; Operations are a verb's, to confirm.
type Resolution struct {
	Kind       string
	Why        string
	Params     map[string]string
	Unit       *unit.Unit
	Operations []AskOp
}

// Resolve is text as a pasted address, a known verb's operations on what it names, or a search; a ref that
// names more than one thing goes to the agent, never guessed. The text after a verb's last ref is the guidance
// of each action it runs.
func (r Resolver) Resolve(ctx context.Context, text string) (Resolution, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Resolution{}, ErrNoText
	}
	if len(fields) == 1 && looksLikeRef(fields[0]) {
		return r.lone(ctx, fields[0])
	}
	verb, ok := r.Vocabulary().Match(fields[0])
	if !ok {
		return Resolution{Kind: KindSearch}, nil
	}
	rest := fields[1:]
	refs, guidance := splitGuidance(text, rest)
	if len(refs) == 0 {
		return r.onText(verb, strings.Join(rest, " ")), nil
	}
	var ops []AskOp
	for _, rt := range refs {
		token := rt.token
		found, err := r.Refs.Find(ctx, token)
		if err == nil && len(found) == 0 && rt.joined >= 0 {
			guidance = strings.TrimSpace(text[rt.joined:])
			break
		}
		switch {
		case err != nil:
			return Resolution{}, err
		case len(found) > 1:
			return Resolution{Kind: KindAgent, Why: WhyAmbiguous, Params: map[string]string{"ref": token}}, nil
		}
		op, ok := operation(verb, found)
		if !ok {
			return Resolution{Kind: KindSearch, Why: WhyNoRef, Params: map[string]string{"ref": token}}, nil
		}
		ops = append(ops, op)
	}
	for i := range ops {
		if ops[i].Op == OpAction {
			ops[i].Guidance = guidance
		}
	}
	return Resolution{Kind: KindVerb, Operations: ops}, nil
}

// lone is what one pasted or typed ref names: its unit, or the task's own unit for a task with none; the
// agent when it names more than one; search, saying why, when it names nothing.
func (r Resolver) lone(ctx context.Context, token string) (Resolution, error) {
	found, err := r.Refs.Find(ctx, token)
	switch {
	case err != nil:
		return Resolution{}, err
	case len(found) > 1:
		return Resolution{Kind: KindAgent, Why: WhyAmbiguous, Params: map[string]string{"ref": token}}, nil
	case len(found) == 0 && strings.Contains(token, "://"):
		return Resolution{Kind: KindSearch, Why: WhyUnclaimed, Params: map[string]string{"address": token}}, nil
	case len(found) == 0:
		return Resolution{Kind: KindSearch, Why: WhyNoRef, Params: map[string]string{"ref": token}}, nil
	}
	u := task.UnitOf(found[0].Task)
	if found[0].Unit != nil {
		u = *found[0].Unit
	}
	return Resolution{Kind: KindUnit, Unit: &u}, nil
}

// onText is verb's op on text that names no unit or task.
func (r Resolver) onText(verb Verb, text string) Resolution {
	switch {
	case text == "" || verb.Op == "":
		return Resolution{Kind: KindSearch, Why: WhyNothingNamed, Params: map[string]string{"verb": verb.ID}}
	case verb.Op == OpCapture:
		return Resolution{Kind: KindVerb, Operations: []AskOp{{Op: OpCapture, Title: text}}}
	}
	return Resolution{Kind: KindVerb, Operations: []AskOp{{Op: OpStart, Brief: text}}}
}

// operation is verb's operation on found, the one thing a ref names; false when there is none or the verb
// cannot run on it.
func operation(verb Verb, found []Ref) (AskOp, bool) {
	if len(found) != 1 {
		return AskOp{}, false
	}
	ref := found[0]
	switch {
	case verb.Action != "" && ref.Unit != nil:
		u := *ref.Unit
		return AskOp{Op: OpAction, Unit: &u, Action: verb.Action}, true
	case verb.TaskVerb != "" && ref.Task != "":
		return AskOp{Op: OpVerb, Task: string(ref.Task), Verb: verb.TaskVerb}, true
	}
	return AskOp{}, false
}

// refToken is a ref a verb names; joined is where the guidance starts when it names nothing and a lone word
// joined it to the ref before ("a#1 and a#2", "a#1 read https://..."), -1 when no word did.
type refToken struct {
	token  string
	joined int
}

// splitGuidance reads the fields after a verb as its refs, then its guidance: the guidance starts at the first
// plain word after a ref that is not a lone word joining two refs, and runs to the end of text, refs and paths
// included.
func splitGuidance(text string, fields []string) (refs []refToken, guidance string) {
	at, join := 0, -1
	for i, f := range fields {
		start := strings.Index(text[at:], f) + at
		at = start + len(f)
		switch {
		case looksLikeRef(f):
			refs = append(refs, refToken{token: f, joined: join})
			join = -1
		case len(refs) > 0 && (i+1 == len(fields) || !looksLikeRef(fields[i+1])):
			return refs, strings.TrimSpace(text[start:])
		case len(refs) > 0:
			join = start
		}
	}
	return refs, ""
}

// looksLikeRef says whether token is written as an address, a unit or a task id rather than a word.
func looksLikeRef(token string) bool {
	if strings.HasSuffix(token, ":") {
		return false
	}
	return strings.ContainsAny(token, "#/:") || strings.HasPrefix(token, task.IDPrefix)
}
