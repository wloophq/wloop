package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/termfmt"
)

const turnShowUsage = "usage: turn show <pr-url> [--json] [--color auto|always|never]"

// optionIDCap caps the option id column; a longer id takes its line alone.
const optionIDCap = 20

// runTurnShow prints the pull request's book: each turn with its records
// and answers, as JSON (with the stored instructions) off a terminal or
// with --json.
func runTurnShow(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("turn show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the book as JSON, as off a terminal")
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the book out for a terminal: auto, always or never")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, turnShowUsage)
	}
	if fs.NArg() == 0 {
		return errors.New(turnShowUsage)
	}
	url := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return fmt.Errorf("%w\n%s", err, turnShowUsage)
	}
	if fs.NArg() > 0 {
		return errors.New(turnShowUsage)
	}
	ref, err := pr.ParseURL(url)
	if err != nil {
		return err
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	book, err := c.TurnBook(ctx, ref)
	if err != nil {
		return err
	}
	if t := e.term(color); t.TTY && !*asJSON {
		writeBook(e.Out, t, book, time.Local)
		return nil
	}
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(book)
}

// bookView writes a book for a person, its times in loc.
type bookView struct {
	t     termfmt.Term
	w     io.Writer
	width int
	loc   *time.Location
}

func writeBook(w io.Writer, t termfmt.Term, b api.TurnBook, loc *time.Location) {
	v := bookView{t, w, reportWidth(t), loc}
	turns := "turns"
	if len(b.Turns) == 1 {
		turns = "turn"
	}
	fmt.Fprintln(w, t.Link(b.PR.URL(), t.Bold(b.PR.String()))+"  "+t.Dim(fmt.Sprintf("%d %s", len(b.Turns), turns)))
	for _, s := range b.Turns {
		fmt.Fprintln(w)
		v.turn(s)
	}
}

func (v bookView) stateStyle(s string) func(string) string {
	switch dturn.State(s) {
	case dturn.Done, dturn.Settled:
		return v.t.Green
	case dturn.Asked:
		return v.t.Yellow
	case dturn.Failed, dturn.Unrecorded:
		return v.t.Red
	}
	return v.t.Dim
}

// stamp is at in the view's zone: the time alone on day's date, else the
// date too.
func (v bookView) stamp(at time.Time, day time.Time) string {
	at, day = at.In(v.loc), day.In(v.loc)
	if at.Format(time.DateOnly) == day.Format(time.DateOnly) {
		return at.Format("15:04")
	}
	return at.Format("01-02 15:04")
}

// entry is a record or an answer of a turn, in the order they happened.
type entry struct {
	at     time.Time
	record *record.Record
	answer *dturn.Answer
}

func (v bookView) turn(s api.TurnShown) {
	tu := s.Turn
	head := v.t.Bold(fmt.Sprintf("#%d", tu.Seq)) + " " + termfmt.Clean(string(tu.Intent)) + "  " + v.stateStyle(string(tu.State))(string(tu.State)) + "  " + v.t.Dim(string(tu.Trigger))
	var times []string
	if !tu.SentAt.IsZero() {
		times = append(times, "sent "+tu.SentAt.In(v.loc).Format("2006-01-02 15:04"))
	}
	if !tu.StartedAt.IsZero() {
		times = append(times, "started "+v.stamp(tu.StartedAt, tu.SentAt))
	}
	if !tu.EndedAt.IsZero() {
		times = append(times, "ended "+v.stamp(tu.EndedAt, tu.SentAt))
	}
	v.print(hanging(v.width, head+"  ", strings.Join(times, " · "), v.t.Dim))
	meta := []string{"round " + tu.ID}
	if tu.Session != "" {
		meta = append(meta, "session "+tu.Session)
	}
	if tu.Agent != "" {
		meta = append(meta, strings.TrimSpace(tu.Agent+" "+tu.AgentSession))
	}
	if tu.ResumeOf != 0 {
		meta = append(meta, fmt.Sprintf("resumes #%d", tu.ResumeOf))
	}
	if tu.ReplacedBy != 0 {
		meta = append(meta, fmt.Sprintf("replaced by #%d", tu.ReplacedBy))
	}
	for _, l := range termfmt.Wrap(strings.Join(meta, " · "), v.width-4, v.width-4) {
		fmt.Fprintln(v.w, "    "+v.t.Dim(l))
	}
	if tu.Guidance != "" {
		v.print(hanging(v.width, "    guidance  ", tu.Guidance, plain))
	}

	var entries []entry
	for i := range s.Records {
		entries = append(entries, entry{at: s.Records[i].At, record: &s.Records[i].Record})
	}
	for i := range s.Answers {
		entries = append(entries, entry{at: s.Answers[i].At, answer: &s.Answers[i]})
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return a.at.Compare(b.at) })
	tw, lw := 0, 0
	for _, en := range entries {
		tw = max(tw, termfmt.Cells(v.stamp(en.at, tu.SentAt)))
		label := "answer"
		if en.record != nil {
			label = termfmt.Clean(string(en.record.State))
		}
		lw = max(lw, termfmt.Cells(label))
	}
	col := 4 + tw + 2 + lw + 2
	for _, en := range entries {
		stamp := v.stamp(en.at, tu.SentAt)
		lead := "    " + v.t.Dim(stamp) + strings.Repeat(" ", tw-termfmt.Cells(stamp)+2)
		if en.answer != nil {
			v.print(hanging(v.width, lead+v.t.Cyan("answer")+strings.Repeat(" ", lw-len("answer")+2), answerText(*en.answer), plain))
			continue
		}
		r := en.record
		state := termfmt.Cut(string(r.State), lw)
		v.print(hanging(v.width, lead+v.stateStyle(string(r.State))(state)+strings.Repeat(" ", lw-termfmt.Cells(state)+2), r.Summary, plain))
		v.record(col, *r)
	}
}

func (v bookView) print(lines []string) {
	for _, l := range lines {
		fmt.Fprintln(v.w, l)
	}
}

// record writes what a record carries beyond its summary at col: the
// outcome and the question with its options.
func (v bookView) record(col int, r record.Record) {
	col = textColumn(v.width, col)
	pad := strings.Repeat(" ", col)
	if o := r.Outcome; o != nil {
		text := fmt.Sprintf("%s · %d blocking · %d non-blocking", o.Verdict, o.Blocking, o.NonBlocking)
		v.print(hanging(v.width, pad, text, func(s string) string { return v.t.Link(o.URL, v.t.Dim(s)) }))
	}
	q := r.Question
	if q == nil {
		return
	}
	v.print(hanging(v.width, pad+v.t.Yellow("?")+" ", q.Text, plain))
	iw := 0
	for _, o := range q.Options {
		iw = max(iw, min(termfmt.Cells(termfmt.Clean(o.ID)), optionIDCap))
	}
	optCol := col + 2 + iw + 2
	if v.width-optCol < minRoom {
		optCol = col + 4
	}
	for _, o := range q.Options {
		id := termfmt.Clean(o.ID)
		style := plain
		if o.ID == q.Recommend {
			style = v.t.Green
		}
		text := o.Label
		if o.Consequence != "" {
			text += ": " + o.Consequence
		}
		above, head := pathHead(v.width, pad+"  ", col+2, id, style)
		lines := append(above, columned(v.width, head, optCol, text, plain)...)
		if o.ID == q.Recommend {
			const mark = "  recommended"
			if last := lines[len(lines)-1]; termfmt.Cells(termfmt.Plain(last))+termfmt.Cells(mark) <= v.width {
				lines[len(lines)-1] = last + "  " + v.t.Green("recommended")
			} else {
				lines = append(lines, strings.Repeat(" ", textColumn(v.width, optCol))+v.t.Green("recommended"))
			}
		}
		v.print(lines)
	}
	if len(q.Options) == 0 && q.Recommend != "" {
		v.print(hanging(v.width, pad+"  ", "yes or no, recommends "+q.Recommend, v.t.Dim))
	}
}

// answerText is how an answer was routed, from where, and what it said.
func answerText(a dturn.Answer) string {
	said := a.Text
	for choice, on := range map[string]bool{"yes": a.Yes, "no": a.No} {
		if on {
			said = strings.TrimSuffix(choice+". "+a.Text, ". ")
		}
	}
	text := cmp.Or(string(a.Route), "pending")
	if a.Source != "" {
		text += " via " + string(a.Source)
	}
	if said != "" {
		text += ": " + said
	}
	return text
}
