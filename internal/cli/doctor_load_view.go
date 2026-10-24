package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/termfmt"
)

// loadGroups are the sections of doctor --load, by scope prefix; a scope
// with none of them goes under Other.
var loadGroups = []struct{ prefix, title string }{{"job:", "Jobs"}, {"loop:", "Loops"}, {"route:", "Routes"}}

// writeLoad writes the meter report for a person: rates per hour first, then
// one table per kind of scope, a nested scope indented under its parent.
func (e *Env) writeLoad(t termfmt.Term, rep api.MeterReport) {
	v := e.doctorView(t)
	rated, ratedSince := rep.Scopes, rep.Since
	if rep.Recent != nil {
		rated, ratedSince = rep.Recent.Scopes, rep.Recent.Since
	}
	window := max(rep.At.Sub(ratedSince), time.Minute).Round(time.Minute)
	v.title("Load", []tally{
		{"up since " + rep.Since.Local().Format("2006-01-02 15:04") + " · " + compactDuration(rep.At.Sub(rep.Since)), t.Dim},
		{"rates over the last " + strings.TrimSuffix(window.String(), "0s"), t.Dim},
	})
	hours := window.Hours()
	for _, line := range v.t.Columns(v.width, 2, []termfmt.Column{{}, {Right: true}, {Right: true}, {Right: true}, {Flex: true}}, loadRates(t, rated, hours)) {
		fmt.Fprintln(v.w, "  "+line)
	}
	groups := map[string][]api.MeterScope{}
	for _, sc := range rep.Scopes {
		title := "Other"
		for _, g := range loadGroups {
			if strings.HasPrefix(sc.Name, g.prefix) {
				title = g.title
				break
			}
		}
		groups[title] = append(groups[title], sc)
	}
	for _, title := range []string{"Jobs", "Loops", "Routes", "Other"} {
		scopes := groups[title]
		if len(scopes) == 0 {
			continue
		}
		fmt.Fprintln(v.w)
		v.title(title, []tally{{fmt.Sprintf("%d scopes", len(scopes)), t.Dim}})
		for _, line := range loadTable(t, v.width-2, scopes) {
			fmt.Fprintln(v.w, "  "+line)
		}
	}
	fmt.Fprintln(v.w)
}

// loadColumns are the resources the tables show, those that spawn processes
// or spend rate limit first.
var loadColumns = []meter.Resource{meter.Git, meter.GitHub, meter.Workspace, meter.Agent, meter.SQL, meter.Tx}

func loadRates(t termfmt.Term, scopes []api.MeterScope, hours float64) [][]termfmt.Cell {
	rate := func(n int64) string { return compactCount(int64(float64(n)/hours+0.5)) + "/h" }
	var rows [][]termfmt.Cell
	for _, res := range loadColumns {
		type share struct {
			name  string
			calls int64
		}
		var all api.MeterStat
		var shares []share
		for _, sc := range scopes {
			if isPhase(sc.Name) {
				continue
			}
			s := resourceTotal(sc.Stats, string(res))
			all.Calls, all.Spawns, all.Points = all.Calls+s.Calls, all.Spawns+s.Spawns, all.Points+s.Points
			if s.Calls > 0 {
				shares = append(shares, share{sc.Name, s.Calls})
			}
		}
		if all.Calls == 0 {
			continue
		}
		slices.SortFunc(shares, func(a, b share) int { return cmp.Or(cmp.Compare(b.calls, a.calls), strings.Compare(a.name, b.name)) })
		var top []string
		for _, sh := range shares[:min(3, len(shares))] {
			top = append(top, fmt.Sprintf("%s %d%%", sh.name, (sh.calls*100+all.Calls/2)/all.Calls))
		}
		spawns, points := termfmt.Cell{}, termfmt.Cell{}
		if all.Spawns > 0 {
			spawns = termfmt.Cell{Text: rate(all.Spawns) + " spawns", Style: t.Yellow}
		}
		if all.Points > 0 {
			points = termfmt.Cell{Text: rate(all.Points) + " pts"}
		}
		rows = append(rows, []termfmt.Cell{{Text: string(res), Style: t.Bold}, {Text: rate(all.Calls)}, spawns, points, {Text: strings.Join(top, ", "), Style: t.Dim}})
	}
	return rows
}

func loadTable(t termfmt.Term, width int, scopes []api.MeterScope) []string {
	slices.SortFunc(scopes, func(a, b api.MeterScope) int { return strings.Compare(a.Name, b.Name) })
	peak := map[meter.Resource]int64{}
	for _, sc := range scopes {
		for _, res := range loadColumns {
			peak[res] = max(peak[res], resourceTotal(sc.Stats, string(res)).Calls)
		}
	}
	shown := slices.DeleteFunc(slices.Clone(loadColumns), func(res meter.Resource) bool { return peak[res] == 0 })
	cols := []termfmt.Column{{Flex: true, Max: 44}, {Right: true}, {Right: true}}
	head := []termfmt.Cell{{Text: "scope", Style: t.Dim}, {Text: "runs", Style: t.Dim}, {Text: "time", Style: t.Dim}}
	for _, res := range shown {
		cols = append(cols, termfmt.Column{Right: true})
		head = append(head, termfmt.Cell{Text: string(res), Style: t.Dim})
	}
	rows := [][]termfmt.Cell{head}
	for _, sc := range scopes {
		name := sc.Name[strings.Index(sc.Name, ":")+1:]
		if parent, child, nested := strings.Cut(name, "/"); nested && slices.ContainsFunc(scopes, func(o api.MeterScope) bool { return strings.HasSuffix(o.Name, ":"+parent) }) {
			name = "  " + child
		}
		row := []termfmt.Cell{{Text: name}, {Text: compactCount(sc.Runs)}, {Text: compactDuration(time.Duration(sc.WallMS) * time.Millisecond), Style: t.Dim}}
		for _, res := range shown {
			s := resourceTotal(sc.Stats, string(res))
			switch {
			case s.Calls == 0:
				row = append(row, termfmt.Cell{Text: "·", Style: t.Dim})
			case s.Calls == peak[res] && len(scopes) > 1:
				row = append(row, termfmt.Cell{Text: compactCount(s.Calls), Style: t.Bold})
			default:
				row = append(row, termfmt.Cell{Text: compactCount(s.Calls)})
			}
		}
		rows = append(rows, row)
	}
	return t.Columns(width, 2, cols, rows)
}

// isPhase says a scope is a phase of a job or a loop, whose cost the report
// already counts in its parent's.
func isPhase(name string) bool {
	return (strings.HasPrefix(name, "job:") || strings.HasPrefix(name, "loop:")) && strings.Contains(name, "/")
}

func resourceTotal(stats []api.MeterStat, res string) api.MeterStat {
	var t api.MeterStat
	for _, st := range stats {
		if st.Res == res {
			t.Calls, t.Spawns, t.Points = t.Calls+st.Calls, t.Spawns+st.Spawns, t.Points+st.Points
		}
	}
	return t
}

func compactCount(n int64) string {
	switch {
	case n < 10_000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}

func compactDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
