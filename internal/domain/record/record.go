// Package record is how an agent says a turn of the loop ended: the state, a
// one-line summary, and when it stops on a question, the whole question.
package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type State string

const (
	Done            State = "done"
	Settled         State = "settled"
	Asked           State = "asked"
	Failed          State = "failed"
	UnrecordedState State = "unrecorded"
)

func (s State) ClosesTurn() bool { return s != Asked }
func (s State) Stopped() bool    { return s == Failed || s == UnrecordedState }

const (
	MaxBytes       = 256 << 10
	SummaryMax     = 200
	TextMax        = 4000
	LabelMax       = 60
	ConsequenceMax = 300
	RecommendMax   = 300
	BodyMax        = 60000
	ItemsMax       = 100
	PathMax        = 300
	ItemBodyMax    = 4000
	ResumeMax      = 4000
	NextMax        = 1000
	URLMax         = 500
	LedgerMax      = 50
)

var (
	verdicts = []string{"APPROVE", "COMMENT", "REQUEST_CHANGES"}
)

type Record struct {
	State    State     `json:"state"`
	Summary  string    `json:"summary"`
	Outcome  *Outcome  `json:"outcome,omitempty"`
	Question *Question `json:"question,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`
	Resume   string    `json:"resume,omitempty"`
	Next     string    `json:"next,omitempty"`
	User     string    `json:"user,omitempty"`
	// Suspicious says the turn stopped on text from someone other than the
	// user that looks like an attempt to steer the agent.
	Suspicious bool `json:"suspicious,omitempty"`
	// Ledger is the items the turn created, assigned, resolved or reopened;
	// an op without a unit is on the turn's unit.
	Ledger []items.Op `json:"ledger,omitempty"`
}

type Outcome struct {
	Verdict     string `json:"verdict"`
	URL         string `json:"url"`
	Blocking    int    `json:"blocking"`
	NonBlocking int    `json:"non_blocking"`
}

type Question struct {
	Kind      string   `json:"kind"`
	Text      string   `json:"text"`
	Options   []Option `json:"options,omitempty"`
	Recommend string   `json:"recommend,omitempty"`
	Publishes bool     `json:"publishes,omitempty"`
}

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
	Publishes   bool   `json:"publishes,omitempty"`
}

func (o Option) Answer() string { return o.Label + " (option " + o.ID + ")" }

func (q Question) Endorses(optionID string, yes bool) bool {
	if q.Kind == "confirm" {
		return q.Publishes && yes
	}
	for _, o := range q.Options {
		if o.ID == optionID {
			return o.Publishes
		}
	}
	return false
}

type Artifact struct {
	Kind  string `json:"kind"`
	Body  string `json:"body"`
	Items []Item `json:"items,omitempty"`
}

type Item struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

func (r Record) EventData() string { return string(r.State) + ": " + r.Headline() }

// EventState is the state a turn_ended event's data, EventData's text, names.
func EventState(data string) State {
	s, _, _ := strings.Cut(data, ": ")
	return State(s)
}

// SuspectedInjection opens the headline of a suspicious record.
const SuspectedInjection = "Suspected prompt injection: "

// Headline is the summary as the user reads it: a suspicious record says
// so first.
func (r Record) Headline() string {
	if r.Suspicious {
		return SuspectedInjection + r.Summary
	}
	return r.Summary
}

type Problems []string

func (p Problems) Error() string { return strings.Join(p, "\n") }

func Parse(raw []byte) (Record, error) {
	if len(raw) > MaxBytes {
		return Record{}, fmt.Errorf("the record is %d bytes; at most %d", len(raw), MaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Record
	if err := dec.Decode(&r); err != nil {
		return Record{}, fmt.Errorf("the record is not valid JSON for a turn end: %w", err)
	}
	if dec.More() {
		return Record{}, errors.New("the record holds more than one JSON value")
	}
	return r, nil
}

var optionID = regexp.MustCompile(`^[a-z0-9-]{1,20}$`)

type checker struct{ p Problems }

func (c *checker) bad(field, format string, args ...any) {
	c.p = append(c.p, field+": "+fmt.Sprintf(format, args...))
}

// text checks length and characters; multiline allows \n and \t.
func (c *checker) text(field, s string, min, max int, multiline bool) {
	n := utf8.RuneCountInString(s)
	switch {
	case n < min:
		c.bad(field, "required")
	case n > max:
		c.bad(field, "%d characters; at most %d", n, max)
	}
	for _, r := range s {
		if (r == '\n' || r == '\t') && multiline {
			continue
		}
		if dropControl(r) == -1 {
			c.bad(field, "control character %U", r)
			return
		}
	}
}

// Validate checks the record against the rules of the turn's intent.
func (r Record) Validate(rules dintent.RecordRules) error {
	return r.validate(&rules)
}

// ValidateShape applies the rules that do not depend on the intent.
func (r Record) ValidateShape() error { return r.validate(nil) }

// ArtifactKinds is the table of artifact kinds an intent's rules list from.
var ArtifactKinds = []string{"review-draft", "triage"}

// OutcomeFields are the outcome fields a rule can list or require.
var OutcomeFields = []string{"verdict", "url"}

func (r Record) validate(rules *dintent.RecordRules) error {
	c := &checker{}
	states := []string{string(Done), string(Settled), string(Asked), string(Failed)}
	if rules != nil {
		states = rules.States
	}
	if !slices.Contains(states, string(r.State)) {
		c.bad("state", "%s, not %q", joinOr(states), r.State)
	}
	switch sum := strings.TrimSpace(r.Summary); {
	case strings.ContainsAny(r.Summary, "\r\n"):
		c.bad("summary", "one line")
	case strings.HasPrefix(sum, "#") || strings.Contains(sum, "endorsed:"):
		c.bad("summary", "plain text: no leading # and no \"endorsed:\"")
	default:
		c.text("summary", strings.TrimSpace(r.Summary), 1, SummaryMax, false)
	}
	if rules != nil {
		r.checkRequired(c, rules)
	}
	if o := r.Outcome; o != nil {
		if rules != nil {
			checkListed(c, o, rules.Outcome)
		}
		if o.Verdict != "" && !slices.Contains(verdicts, o.Verdict) {
			c.bad("outcome.verdict", "APPROVE, COMMENT or REQUEST_CHANGES, not %q", o.Verdict)
		}
		c.text("outcome.url", o.URL, 0, URLMax, false)
		if o.Blocking < 0 {
			c.bad("outcome.blocking", "zero or more")
		}
		if o.NonBlocking < 0 {
			c.bad("outcome.non_blocking", "zero or more")
		}
	}
	switch {
	case r.State == Asked && r.Question == nil:
		c.bad("question", "required with asked")
	case r.State != Asked && r.Question != nil:
		c.bad("question", "only with asked")
	case r.Question != nil:
		r.Question.check(c)
	}
	if r.Suspicious && r.State != Asked {
		c.bad("suspicious", "only with asked")
	}
	if r.State == Asked || r.State == Failed {
		c.text("resume", strings.TrimSpace(r.Resume), 1, ResumeMax, true)
	} else {
		c.text("resume", r.Resume, 0, ResumeMax, true)
	}
	c.text("next", r.Next, 0, NextMax, true)
	c.text("user", r.User, 0, NextMax, true)
	if a := r.Artifact; a != nil {
		switch {
		case rules != nil && len(rules.Artifacts) == 0:
			c.bad("artifact.kind", "this turn's intent records no artifact")
		case rules != nil && !slices.Contains(rules.Artifacts, a.Kind):
			c.bad("artifact.kind", "artifact kind %s is not one of %s", a.Kind, strings.Join(rules.Artifacts, ", "))
		case rules == nil && !slices.Contains(ArtifactKinds, a.Kind):
			c.bad("artifact.kind", "%s, not %q", joinOr(ArtifactKinds), a.Kind)
		}
		c.text("artifact.body", a.Body, 0, BodyMax, true)
		if len(a.Items) > ItemsMax {
			c.bad("artifact.items", "%d items; at most %d", len(a.Items), ItemsMax)
		}
		for i, it := range a.Items {
			f := fmt.Sprintf("artifact.items[%d]", i)
			c.text(f+".path", it.Path, 1, PathMax, false)
			c.text(f+".body", it.Body, 1, ItemBodyMax, true)
			if it.Line <= 0 {
				c.bad(f+".line", "a positive line")
			}
		}
	}
	r.checkLedger(c)
	if len(c.p) == 0 {
		return nil
	}
	return c.p
}

// anyUnit stands in for the turn's unit, which a record's op may leave out.
var anyUnit = unit.Unit{Provider: "-", Key: "-"}

func (r Record) checkLedger(c *checker) {
	if len(r.Ledger) > LedgerMax {
		c.bad("ledger", "%d ops; at most %d", len(r.Ledger), LedgerMax)
		return
	}
	for i, o := range r.Ledger {
		if o.Unit.IsZero() {
			o.Unit = anyUnit
		}
		var bad items.Problems
		if errors.As(o.ValidateShape(), &bad) {
			for _, p := range bad {
				c.p = append(c.p, fmt.Sprintf("ledger[%d] %s", i, p))
			}
		}
	}
}

func checkListed(c *checker, o *Outcome, listed []string) {
	if o.Verdict != "" && !slices.Contains(listed, "verdict") {
		c.bad("outcome.verdict", "this turn's intent records no verdict")
	}
	if o.URL != "" && !slices.Contains(listed, "url") {
		c.bad("outcome.url", "this turn's intent records no url")
	}
	if (o.Blocking != 0 || o.NonBlocking != 0) && !slices.Contains(listed, "verdict") {
		c.bad("outcome.blocking", "the counts go with a verdict, which this turn's intent does not record")
	}
}

func (r Record) checkRequired(c *checker, rules *dintent.RecordRules) {
	for _, req := range rules.Require {
		if string(r.State) != req.State || (req.Artifact != "" && (r.Artifact == nil || r.Artifact.Kind != req.Artifact)) {
			continue
		}
		for _, f := range req.Fields {
			if !slices.Contains(OutcomeFields, f) {
				c.bad("outcome."+f, "no such outcome field; one of %s", joinOr(OutcomeFields))
				continue
			}
			if f == "verdict" && (r.Outcome == nil || r.Outcome.Verdict == "") || f == "url" && (r.Outcome == nil || r.Outcome.URL == "") {
				doc := strings.Join(strings.Fields(rules.OutcomeDoc), " ")
				if req.Artifact != "" {
					doc = ""
				}
				c.bad("outcome."+f, "%s", requirement(req, f, doc))
			}
		}
	}
}

func requirement(req dintent.Require, field, doc string) string {
	what := "a record in state " + req.State
	if req.Artifact != "" {
		what += " with a " + req.Artifact + " artifact"
	}
	msg := what + " names its " + field
	if doc != "" {
		msg += ": " + doc
	}
	return msg
}

func joinOr(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

func (q Question) check(c *checker) {
	c.text("question.text", strings.TrimSpace(q.Text), 1, TextMax, true)
	switch q.Kind {
	case "choice":
		if len(q.Options) < 2 || len(q.Options) > 5 {
			c.bad("question.options", "2 to 5 options, not %d", len(q.Options))
		}
		seen := map[string]bool{}
		for i, o := range q.Options {
			f := fmt.Sprintf("question.options[%d]", i)
			switch {
			case !optionID.MatchString(o.ID):
				c.bad(f+".id", "lowercase letters, digits and dashes, up to 20")
			case seen[o.ID]:
				c.bad(f+".id", "repeats %q", o.ID)
			}
			seen[o.ID] = true
			c.text(f+".label", o.Label, 1, LabelMax, false)
			c.text(f+".consequence", o.Consequence, 1, ConsequenceMax, false)
		}
		if q.Recommend != "" && !seen[q.Recommend] {
			c.bad("question.recommend", "names no option: %q", q.Recommend)
		}
	case "confirm", "free":
		if len(q.Options) > 0 {
			c.bad("question.options", "only for choice")
		}
		if q.Publishes && q.Kind != "confirm" {
			c.bad("question.publishes", "only on a confirm")
		}
		if q.Kind == "confirm" && q.Recommend != "" && q.Recommend != "yes" && q.Recommend != "no" {
			c.bad("question.recommend", "yes or no for a confirm")
		}
		c.text("question.recommend", q.Recommend, 0, RecommendMax, false)
	default:
		c.bad("question.kind", "choice, confirm or free, not %q", q.Kind)
	}
}

// Unrecorded is the loop's own record for a turn the agent ended without one,
// built from the agent's last message.
func Unrecorded(message string) Record {
	summary := "the agent ended its turn without a record"
	for line := range strings.SplitSeq(message, "\n") {
		if l := strings.TrimSpace(strings.Map(dropControl, line)); l != "" {
			summary = cut(l, SummaryMax)
			break
		}
	}
	keep := func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		return dropControl(r)
	}
	return Record{State: UnrecordedState, Summary: summary, Resume: cut(strings.TrimSpace(strings.Map(keep, message)), ResumeMax)}
}

func dropControl(r rune) rune {
	if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
		return -1
	}
	return r
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (r Record) Hash() string {
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
