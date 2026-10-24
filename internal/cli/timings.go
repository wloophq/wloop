package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/pr"
)

var timingsUsage = "usage: " + brand.Command + " timings [--since <days>] [--prs] [--turns] [--tags] [--json]"

// timingsView is what the text report adds to its summary.
type timingsView struct {
	days             int
	prs, turns, tags bool
}

func runTimings(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("timings", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var v timingsView
	fs.IntVar(&v.days, "since", 30, "the days to look back")
	fs.BoolVar(&v.prs, "prs", false, "also list each pull request with its milestones")
	fs.BoolVar(&v.turns, "turns", false, "also list each turn with its duration")
	fs.BoolVar(&v.tags, "tags", false, "also list each tag key's values and the dropped spellings")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || v.days < 1 {
		return errors.New(timingsUsage)
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	rep, err := c.Timings(ctx, v.days)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printTimings(e.Out, rep, v)
	return nil
}

// span is a duration as a person reads it.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
}

func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

func quantileRow(w io.Writer, label string, q api.Quantiles) {
	if q.Count == 0 {
		fmt.Fprintf(w, "  %-38s %10s %10s %5d\n", label, "-", "-", 0)
		return
	}
	fmt.Fprintf(w, "  %-38s %10s %10s %5d\n", label, span(ms(q.P50MS)), span(ms(q.P90MS)), q.Count)
}

func count(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func stampOf(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

const viewerUnknown = "unknown: the daemon does not know your GitHub login yet"

func printTimings(w io.Writer, rep api.TimingsReport, v timingsView) {
	fmt.Fprintf(w, brand.Command+" timings over the last %d days, since %s\n", v.days, stampOf(rep.Since))
	switch {
	case rep.RecordedSince.IsZero():
		fmt.Fprintln(w, "recorded since: unknown; this store keeps no start of its timing records")
	case rep.RecordedSince.After(rep.Since):
		fmt.Fprintf(w, "recorded since %s; the days before have no turn, brief or request times\n", stampOf(rep.RecordedSince))
	default:
		fmt.Fprintf(w, "recorded since %s\n", stampOf(rep.RecordedSince))
	}

	header := func(title string) { fmt.Fprintf(w, "\n%-40s %10s %10s %5s\n", title, "p50", "p90", "n") }
	header("pull requests, start and end in the window")
	for _, m := range []struct {
		key, label string
		mine       bool
	}{
		{api.MilestoneReviewWait, "asked of me -> my first review", true},
		{api.MilestoneToApproval, "opened -> first approval", false},
		{api.MilestoneToMerge, "opened -> merged", false},
		{api.MilestoneMineToMerge, "mine: opened -> merged", true},
		{api.MilestoneLeadTime, "lead time: first push -> merged", false},
		{api.MilestoneMineLead, "mine: first push -> merged", true},
		{api.MilestoneInLoop, "in " + brand.Name + ", since recording began", false},
	} {
		if m.mine && rep.Viewer == "" {
			fmt.Fprintf(w, "  %-38s %s\n", m.label, viewerUnknown)
			continue
		}
		quantileRow(w, m.label, rep.Milestones[m.key])
	}

	fmt.Fprintf(w, "\n%-40s %10s %10s %5s %10s %10s\n", "followed pull requests waited on", "total", "so far", "prs", "span p50", "span p90")
	for _, p := range []struct{ key, label string }{
		{api.WaitingOnMe, "me"},
		{api.WaitingOnOthers, "the author or other reviewers"},
		{api.WaitingOnLoop, "the review loop"},
	} {
		s := rep.Waiting[p.key]
		if s.PRs == 0 {
			fmt.Fprintf(w, "  %-38s %10s %10s %5d %10s %10s\n", p.label, "-", "-", 0, "-", "-")
			continue
		}
		soFar, p50, p90 := "-", "-", "-"
		if s.OpenMS > 0 {
			soFar = span(ms(s.OpenMS))
		}
		if s.Spans.Count > 0 {
			p50, p90 = span(ms(s.Spans.P50MS)), span(ms(s.Spans.P90MS))
		}
		fmt.Fprintf(w, "  %-38s %10s %10s %5d %10s %10s\n", p.label, span(ms(s.TotalMS)), soFar, s.PRs, p50, p90)
	}

	header("turns, started to ended")
	intents := slices.DeleteFunc(slices.Sorted(maps.Keys(rep.Turns)), func(k string) bool { return k == api.TurnStartWait || k == api.TurnBriefWait })
	if len(intents) == 0 {
		fmt.Fprintln(w, "  none ended in the window")
	}
	for _, k := range intents {
		quantileRow(w, k, rep.Turns[k])
	}

	header("waits")
	quantileRow(w, "turn sent -> started", rep.Turns[api.TurnStartWait])
	quantileRow(w, "your request -> its turn starting", rep.Intakes)
	quantileRow(w, "brief wanted -> automated turn sent", rep.Turns[api.TurnBriefWait])

	header("briefs since recording began")
	quantileRow(w, "call of an ok brief", rep.Briefs.Call)
	fmt.Fprintf(w, "  %s, $%.2f, %s of the breaker; %s left out\n", plural(rep.Briefs.Failed, "failed", "failed"), rep.Briefs.CostUSD,
		plural(rep.Briefs.Outages, "outage", "outages"), plural(rep.Briefs.Unrecorded, "run from before recording", "runs from before recording"))

	fmt.Fprintf(w, "\n%-40s %10s %10s %12s %12s\n", "turn tokens by agent and model", "input", "output", "cache read", "cache write")
	if len(rep.Tokens) == 0 {
		fmt.Fprintln(w, "  none read yet")
	}
	for _, model := range slices.Sorted(maps.Keys(rep.Tokens)) {
		t := rep.Tokens[model]
		fmt.Fprintf(w, "  %-38s %10s %10s %12s %12s\n", model, count(t.Input), count(t.Output), count(t.CacheRead), count(t.CacheWrite))
	}

	fmt.Fprintf(w, "\nchange failure: %s\n", changeFailureLine(rep.ChangeFailure))

	if v.prs {
		fmt.Fprintf(w, "\npull requests with a turn, a brief, my review, an approval or a merge in the window: %d\n", len(rep.PRs))
		for _, p := range rep.PRs {
			fmt.Fprintf(w, "  %s\n", prLine(p))
		}
	}
	if v.turns {
		fmt.Fprintf(w, "\nturns sent in the window: %d\n", len(rep.TurnList))
		for _, t := range rep.TurnList {
			fmt.Fprintf(w, "  %s\n", turnLine(t))
		}
	}
	if v.tags {
		fmt.Fprintln(w, "\ntags of the current briefs (pull requests per value)")
		for _, k := range slices.Sorted(maps.Keys(rep.Tags)) {
			var vs []string
			for _, v := range rep.Tags[k] {
				vs = append(vs, fmt.Sprintf("%s %d", v.Value, v.Count))
			}
			fmt.Fprintf(w, "  %s: %s\n", k, strings.Join(vs, ", "))
		}
		if len(rep.Dropped) > 0 {
			fmt.Fprintf(w, "dropped spellings: %s\n", strings.Join(rep.Dropped, ", "))
		}
	}
	if !v.prs && !v.turns {
		fmt.Fprintf(w, "\n%s pull requests and %s turns in the window: --prs and --turns list them\n", fmt.Sprint(len(rep.PRs)), fmt.Sprint(len(rep.TurnList)))
	}
}

func changeFailureLine(cf api.ChangeFailure) string {
	if cf.Briefed == 0 {
		return fmt.Sprintf("unknown: no briefed pull request merged at least %d days ago lists its changed files (%d merged in the window, %d too recent); %d tagged change:fix or change:revert",
			cf.Days, cf.Merged, cf.Recent, cf.Tagged)
	}
	return fmt.Sprintf("%d of %d (%.0f%%) %s; %d merged in the window, %d too recent to tell, %d tagged change:fix or change:revert themselves",
		cf.Failed, cf.Briefed, 100*float64(cf.Failed)/float64(cf.Briefed), cf.Definition, cf.Merged, cf.Recent, cf.Tagged)
}

func refName(url string) string {
	if ref, err := pr.ParseURL(url); err == nil {
		return ref.String()
	}
	return url
}

func shortTitle(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return s
}

// prLine is a pull request's milestones, each after the opening.
func prLine(p api.PRTimes) string {
	parts := []string{refName(p.PR)}
	if p.Title != "" {
		parts[0] += "  " + shortTitle(p.Title)
	}
	after := func(label string, t time.Time) {
		switch {
		case t.IsZero():
		case p.Opened.IsZero() || t.Before(p.Opened):
			parts = append(parts, label+" "+stampOf(t))
		default:
			parts = append(parts, label+" +"+span(t.Sub(p.Opened)))
		}
	}
	if !p.Opened.IsZero() {
		parts = append(parts, "opened "+stampOf(p.Opened))
	}
	after("asked of me", p.FirstRequest)
	after("my review", p.FirstReview)
	after("approved", p.Approved)
	after("merged", p.Merged)
	parts = append(parts, plural(p.Turns, "turn", "turns")+", "+plural(p.Briefs, "brief", "briefs"))
	return strings.Join(parts, " · ")
}

// turnLine is one turn: when it went out, how long it ran and its output.
func turnLine(t api.TurnTimes) string {
	parts := []string{fmt.Sprintf("%s #%d %s (%s)", refName(t.PR), t.Seq, t.Intent, t.Trigger), "sent " + stampOf(t.Sent), t.State}
	if t.DurationMS > 0 {
		parts = append(parts, "ran "+span(ms(t.DurationMS)))
	}
	if !t.Started.IsZero() {
		parts = append(parts, "started +"+span(t.Started.Sub(t.Sent)))
	}
	if t.OutputTokens > 0 {
		parts = append(parts, count(t.OutputTokens)+" output tokens")
	}
	if t.Agent != "" {
		parts = append(parts, t.Agent)
	}
	return strings.Join(parts, " · ")
}
