package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TriageMax bounds the triage artifact an entry carries.
const TriageMax = 1500

type Entry struct {
	When                                            time.Time
	Intent, Trigger, State, Verdict, Head, Endorsed string
	Summary, Next, Triage                           string
	Said                                            []string
	// Ledger is one line per op of the turn's records with its outcome.
	Ledger []string
}

// History is one entry per turn but the withdrawn ones, newest first, at most
// limit. Summary, Next and Triage were written by an agent; Said holds what
// the user said: the answers that reached the agent, then the guidance.
func (b *Book) History(limit int) []Entry { return b.HistoryWith(limit, nil) }

// HistoryWith is History with the outcomes the items ledger logged for the
// ops of the turns' records, oldest first.
func (b *Book) HistoryWith(limit int, log []OpOutcome) []Entry {
	m := &outcomes{log: log, used: make([]bool, len(log))}
	lines := map[int64][]string{}
	for _, r := range b.Records {
		for _, o := range LedgerOps(b.Unit, r) {
			lines[r.TurnSeq] = append(lines[r.TurnSeq], opLine(o, m.take(o, r.At)))
		}
	}
	var out []Entry
	for i := len(b.Turns) - 1; i >= 0 && len(out) < limit; i-- {
		if t := b.Turns[i]; t.State != Withdrawn {
			e := b.entry(t)
			e.Ledger = lines[t.Seq]
			out = append(out, e)
		}
	}
	return out
}

// OpOutcome is what the items ledger logged for one op of a turn.
type OpOutcome struct {
	Op              items.Op
	Outcome, Reason string
	At              time.Time
}

// LedgerOps are r's ops as the turns apply them: on u when they name no
// unit, and a create without an id named after its turn, kind and summary,
// so a create restated by a later record of the turn names the same item.
func LedgerOps(u unit.Unit, r Record) []items.Op {
	if len(r.Record.Ledger) == 0 {
		return nil
	}
	out := slices.Clone(r.Record.Ledger)
	seen := map[string]int{}
	for i := range out {
		if out[i].Unit.IsZero() {
			out[i].Unit = u
		}
		if out[i].Op == items.OpCreate && out[i].Item == "" {
			id := createID(r.TurnSeq, out[i].Fields)
			if n := seen[id]; n > 0 {
				out[i].Item = fmt.Sprintf("%s.%d", id, n)
			} else {
				out[i].Item = id
			}
			seen[id]++
		}
	}
	return out
}

func createID(seq int64, f *items.Fields) string {
	var kind items.Kind
	var summary string
	if f != nil && f.Kind != nil {
		kind = *f.Kind
	}
	if f != nil && f.Summary != nil {
		summary = *f.Summary
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%d\x00%s\x00%s", seq, kind, summary))
	return "t" + hex.EncodeToString(sum[:4])
}

type outcomes struct {
	log  []OpOutcome
	used []bool
}

// take is the first unused outcome logged for o at or after since.
func (m *outcomes) take(o items.Op, since time.Time) *OpOutcome {
	for i, e := range m.log {
		if m.used[i] || e.At.Before(since) || e.Op.Op != o.Op || e.Op.Item != o.Item || e.Op.Unit != o.Unit || !sameTo(e.Op.To, o.To) {
			continue
		}
		m.used[i] = true
		return &m.log[i]
	}
	return nil
}

func sameTo(a, b *unit.Unit) bool {
	return (a == nil || a.IsZero()) && (b == nil || b.IsZero()) || a != nil && b != nil && *a == *b
}

var opDone = map[items.OpKind]string{items.OpCreate: "created", items.OpUpdate: "updated", items.OpAssign: "assigned",
	items.OpResolve: "resolved", items.OpReopen: "reopened", items.OpLink: "linked", items.OpEvidence: "added evidence to"}

func opLine(o items.Op, got *OpOutcome) string {
	target := o.Item
	if target == "" {
		target = "the unit"
	}
	if got == nil {
		return fmt.Sprintf("%s %s: no outcome logged", o.Op, target)
	}
	switch got.Outcome {
	case "applied":
		line := opDone[o.Op] + " " + target
		switch {
		case o.Op == items.OpResolve:
			line += ": " + string(o.Resolution)
		case o.Op == items.OpAssign:
			keys := make([]string, len(o.Assignees))
			for i, p := range o.Assignees {
				keys[i] = p.Key
			}
			line += " to " + strings.Join(keys, ", ")
		case o.Op == items.OpLink && o.To != nil:
			line += " to " + o.To.Label()
		}
		return line
	case "noop":
		if got.Reason != "" {
			return fmt.Sprintf("%s %s: no change, %s", o.Op, target, got.Reason)
		}
		return fmt.Sprintf("%s %s: no change", o.Op, target)
	case "proposed":
		return fmt.Sprintf("proposed %s %s to the user", o.Op, target)
	}
	return fmt.Sprintf("refused %s: %s", target, got.Reason)
}

// Published is a verdict a turn published: the head its record covered,
// whether the user stands behind it, and when it was recorded.
type Published struct {
	Verdict, Head, Endorsed string
	At                      time.Time
}

// LastVerdict is the newest verdict a turn of intent published, however far
// back the history it lies.
func (b *Book) LastVerdict(intent Intent) (Published, bool) {
	return b.lastVerdict(func(t Turn) bool { return t.Intent == intent })
}

// LastRoleVerdict is LastVerdict over the turns whose intent held role.
func (b *Book) LastRoleVerdict(role string) (Published, bool) {
	return b.lastVerdict(func(t Turn) bool { return t.Role == role })
}

func (b *Book) lastVerdict(of func(Turn) bool) (Published, bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := b.Turns[i]
		if !of(t) || t.State == Withdrawn || !b.published(t.Seq) {
			continue
		}
		last, _ := b.LastRecord(t.Seq)
		head := last.Head
		if head == "" {
			head = t.Head
		}
		return Published{Verdict: last.Record.Outcome.Verdict, Head: head, Endorsed: b.Endorsed(t.Seq), At: last.At}, true
	}
	return Published{}, false
}

func (b *Book) entry(t Turn) Entry {
	e := Entry{When: t.SentAt, Intent: string(t.Intent), Trigger: string(t.Trigger), State: string(t.State), Head: t.Head}
	if b.published(t.Seq) {
		e.Endorsed = b.Endorsed(t.Seq)
	}
	if last, ok := b.LastRecord(t.Seq); ok {
		r := last.Record
		e.Summary, e.Next = r.Summary, r.Next
		if last.Head != "" {
			e.Head = last.Head
		}
		if r.State == record.Done && r.Outcome != nil {
			e.Verdict = r.Outcome.Verdict
		}
		if t.State == Done && r.Artifact != nil && b.HistoryArtifact != nil && r.Artifact.Kind == b.HistoryArtifact(string(t.Intent)) {
			e.Triage = cut(r.Artifact.Body, TriageMax)
		}
	}
	for _, a := range b.Answers {
		if a.TurnSeq == t.Seq && (a.Route.Noticed() || a.Route == Resumed || a.Route == Direct) {
			e.Said = append(e.Said, b.Said(a))
		}
	}
	if t.Guidance != "" {
		e.Said = append(e.Said, t.Guidance)
	}
	return e
}

// Said is the answer as the agent reads it: the chosen option, yes or no,
// then the text as a note; the text alone when nothing was chosen, and a
// confirm's no when neither was given.
func (b *Book) Said(a Answer) string {
	choice, note := b.choice(a), strings.TrimSpace(a.Text)
	switch {
	case choice == "" && note == "":
		return "no"
	case choice == "":
		return a.Text
	case note == "" || note == choice:
		return choice
	}
	return choice + ". " + note
}

func (b *Book) choice(a Answer) string {
	switch {
	case a.OptionID != "" && a.Posted:
		return a.OptionID
	case a.OptionID != "":
		for i := len(b.Records) - 1; i >= 0; i-- {
			if q := b.Records[i].Record.Question; b.Records[i].TurnSeq == a.TurnSeq && q != nil {
				if j := slices.IndexFunc(q.Options, func(o record.Option) bool { return o.ID == a.OptionID }); j >= 0 {
					return q.Options[j].Answer()
				}
			}
		}
		return a.OptionID
	case a.Yes:
		return "yes"
	case a.No:
		return "no"
	}
	return ""
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
