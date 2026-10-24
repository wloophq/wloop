package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/core/turns"
)

type Options struct {
	Asker    turns.Asker
	Set      Set
	Out      string
	Label    string
	Split    string // dev, holdout or all ("" is all)
	Template string // the extractor's prompt template; "" is the shipped one
	Viewer   extract.Viewer
	Language string
	Now      string // RFC3339, overrides every fixture's now
	MaxUSD   float64
	Jobs     int

	ExtractorTier, JudgeTier turns.Tier
	// EscalateOverChars sends an extraction whose conversation renders longer to the frontier tier; 0 never.
	EscalateOverChars int
	Log               io.Writer
}

type Report struct {
	SpentUSD float64
	Judged   int
	Failed   int
	Score    Score
}

// manifest is every fixture of the run's set and its split, which score
// counts as expected.
type manifest struct {
	Fixtures []manifestEntry `json:"fixtures"`
}

type manifestEntry struct {
	ID    string `json:"id"`
	Split string `json:"split"`
}

func manifestPath(out, label string) string { return filepath.Join(out, label, "manifest.json") }

// pendingRefs marks a template written before the ledger: its schema had
// item ids where the shipped one has refs, so the run states the mapping.
const pendingRefs = `

## Schema notes

Give each item the ` + "`ref`" + ` "n1", "n2", ... in order. Set ` + "`mode`" + ` to "none" and ` + "`place`" + ` to {"context": "", "why": ""}. Leave ` + "`segments`" + ` and ` + "`links`" + ` empty.`

// ExtractorPrompt fills tmpl for in exactly as extract.Prompt fills the
// shipped template; "" is the shipped template itself.
func ExtractorPrompt(tmpl string, in extract.Input) (string, error) {
	if tmpl == "" {
		return extract.Prompt(in)
	}
	if !strings.Contains(tmpl, "{{LEDGER}}") {
		tmpl += pendingRefs
	}
	values := baseValues(in)
	values["LEDGER"] = "(no items yet)"
	values["CONTEXTS"] = "(no context is offered: leave `place.context` empty)"
	values["CONVERSATION"] = extract.Render(in)
	return fill(tmpl, values), nil
}

// baseValues are the placeholders every stage shares, as extract.Prompt
// renders them.
func baseValues(in extract.Input) map[string]string {
	name := cmpOr(in.Viewer.Name, in.Viewer.ID)
	return map[string]string{
		"VIEWER_ID":   in.Viewer.ID,
		"VIEWER_NAME": name,
		"NOW":         in.Now.Format("2006-01-02 15:04 MST (Monday)"),
		"LANGUAGE":    cmpOr(in.Language, "English"),
	}
}

func (o Options) selected() []Fixture {
	var out []Fixture
	for _, f := range o.Set.Fixtures {
		if o.Split == "" || o.Split == "all" || o.Split == f.SplitOf() {
			out = append(out, f)
		}
	}
	return out
}

func (o Options) viewer() (extract.Viewer, error) {
	v := o.Set.Viewer
	if o.Viewer.ID != "" {
		v = o.Viewer
	}
	if v.ID == "" {
		return v, errNoViewer
	}
	return v, nil
}

func (o Options) names() map[string]string {
	out := make(map[string]string, len(o.Set.Users))
	for id, n := range o.Set.Users {
		out[id] = n
	}
	for _, f := range o.Set.Fixtures {
		for _, m := range f.Messages {
			if m.UserID != "" && m.UserName != "" && out[m.UserID] == "" {
				out[m.UserID] = m.UserName
			}
		}
	}
	return out
}

// fixtureRun is everything one fixture's stages share.
type fixtureRun struct {
	f            Fixture
	in           extract.Input
	conversation string
	base         map[string]string
	key          string // the key's now
}

type runner struct {
	o      Options
	budget *budget
	names  map[string]string
}

func (r *runner) prepare(f Fixture, v extract.Viewer) (fixtureRun, error) {
	now, err := f.NowAt(r.o.Now)
	if err != nil {
		return fixtureRun{}, err
	}
	in := f.Input(v, r.names, now, cmpOr(r.o.Language, "English"))
	return fixtureRun{f: f, in: in, conversation: extract.Render(Plain(in)), base: baseValues(in), key: now.Format(time.RFC3339)}, nil
}

func (r *runner) stage(fr fixtureRun, parts ...string) stage {
	return stage{inner: r.o.Asker, budget: r.budget, path: filepath.Join(append(append([]string{r.o.Out}, parts...), fr.f.ID+".json")...),
		language: fr.base["LANGUAGE"], now: fr.key}
}

func (r *runner) judgeAsk(prompt, schema string) turns.Ask {
	return turns.Ask{Tier: r.o.JudgeTier, Prompt: prompt, Schema: schema, MaxCostUSD: JudgeCallUSD}
}

func (r *runner) extractAsk(in extract.Input) turns.Ask {
	return turns.Ask{Tier: extract.TierFor(in, r.o.ExtractorTier, r.o.EscalateOverChars), MaxCostUSD: ExtractorCallUSD}
}

func (r *runner) extractorStage(fr fixtureRun) stage {
	s := r.stage(fr, r.o.Label, "ledger")
	if r.o.Template != "" {
		s.override = func(string) string {
			p, _ := ExtractorPrompt(r.o.Template, fr.in)
			return p
		}
	}
	return s
}

// precheck refuses before any call when a cached oracle or ledger file was
// produced under another key.
func (r *runner) precheck(fr fixtureRun) error {
	oracle := r.judgeAsk(OraclePrompt(fr.base, fr.conversation), briefSchema)
	s := r.stage(fr, "oracle")
	if _, err := s.cached(s.key(oracle)); err != nil {
		return err
	}
	p, err := ExtractorPrompt(r.o.Template, fr.in)
	if err != nil {
		return err
	}
	q := r.extractAsk(fr.in)
	q.Prompt, q.Schema = p, extract.Schema()
	s = r.extractorStage(fr)
	_, err = s.cached(s.key(q))
	return err
}

func (r *runner) one(ctx context.Context, fr fixtureRun) (Judgment, error) {
	ref, err := r.stage(fr, "oracle").Ask(ctx, r.judgeAsk(OraclePrompt(fr.base, fr.conversation), briefSchema))
	if err != nil {
		return Judgment{}, fmt.Errorf("oracle: %w", err)
	}
	es := r.extractorStage(fr)
	res, _, err := extract.Extract(ctx, es, r.extractAsk(fr.in), fr.in)
	if errors.Is(err, turns.ErrUnfit) {
		es.forget()
	}
	if err != nil {
		return Judgment{}, fmt.Errorf("extraction: %w", err)
	}
	view := LedgerView(fr.f, res, r.names, fr.in.Now)
	blind, err := r.stage(fr, r.o.Label, "blind").Ask(ctx, r.judgeAsk(BlindPrompt(fr.base, view), briefSchema))
	if err != nil {
		return Judgment{}, fmt.Errorf("blind: %w", err)
	}
	ans, err := r.stage(fr, r.o.Label, "judge").Ask(ctx, r.judgeAsk(JudgePrompt(fr.base, fr.conversation, ref.JSON, blind.JSON, view), judgeSchema))
	if err != nil {
		return Judgment{}, fmt.Errorf("judge: %w", err)
	}
	var j Judgment
	if err := json.Unmarshal(ans.JSON, &j); err != nil {
		return Judgment{}, fmt.Errorf("judge: %w: %v", turns.ErrUnfit, err)
	}
	return j, nil
}

// Run writes every stage of every selected fixture under Out, then the
// label's score. It refuses before any call when a cached file has another
// key, and stops starting calls before the spend would pass MaxUSD.
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Label == "" {
		return Report{}, errors.New("no label")
	}
	if o.ExtractorTier == "" {
		o.ExtractorTier = turns.TierFast
	}
	if o.JudgeTier == "" {
		o.JudgeTier = turns.TierFrontier
	}
	if o.Jobs < 1 {
		o.Jobs = 1
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	v, err := o.viewer()
	if err != nil {
		return Report{}, err
	}
	r := &runner{o: o, budget: newBudget(o.MaxUSD), names: o.names()}
	var runs []fixtureRun
	for _, f := range o.selected() {
		fr, err := r.prepare(f, v)
		if err != nil {
			return Report{}, err
		}
		if err := r.precheck(fr); err != nil {
			return Report{}, err
		}
		runs = append(runs, fr)
	}
	m := manifest{}
	for _, f := range o.Set.Fixtures {
		m.Fixtures = append(m.Fixtures, manifestEntry{ID: f.ID, Split: f.SplitOf()})
	}
	if err := writeJSON(manifestPath(o.Out, o.Label), m); err != nil {
		return Report{}, err
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		mu  sync.Mutex
		rep Report
		wg  sync.WaitGroup
	)
	work := make(chan fixtureRun)
	for range o.Jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fr := range work {
				j, err := r.one(ctx, fr)
				var stale *StaleError
				if errors.As(err, &stale) {
					cancel(err)
				}
				mu.Lock()
				if err != nil {
					rep.Failed++
					fmt.Fprintf(o.Log, "%s: %v\n", fr.f.ID, err)
				} else {
					rep.Judged++
					fmt.Fprintf(o.Log, "%s: same_actions=%t severity=%s\n", fr.f.ID, j.SameActions, j.Severity)
				}
				mu.Unlock()
			}
		}()
	}
	for _, fr := range runs {
		if ctx.Err() != nil {
			break
		}
		select {
		case work <- fr:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()

	spent, stopped := r.budget.state()
	rep.SpentUSD = spent
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return rep, err
	}
	if rep.Score, err = WriteScore(o.Out, o.Label); err != nil {
		return rep, err
	}
	fmt.Fprintf(o.Log, "%s: $%.2f spent, %d judged, %d failed\n", o.Label, spent, rep.Judged, rep.Failed)
	switch {
	case stopped:
		return rep, fmt.Errorf("%w ($%.2f of $%.2f spent)", ErrBudget, spent, o.MaxUSD)
	case rep.Failed > 0:
		return rep, fmt.Errorf("%d of %d fixtures failed", rep.Failed, len(runs))
	}
	return rep, nil
}
