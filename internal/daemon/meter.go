package daemon

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/meter"
)

const topScopes = 2

func wireStat(res meter.Resource, op string, s meter.Stat) api.MeterStat {
	return api.MeterStat{
		Res: string(res), Op: op, Calls: s.Calls, Spawns: s.Spawns, Errors: s.Errors, NotModified: s.NotModified,
		Points: s.Points, Bytes: s.Bytes, Rows: s.Rows, DurMS: s.Dur.Milliseconds(), MaxMS: s.Max.Milliseconds(),
	}
}

func wireTally(t meter.Tally) []api.MeterStat {
	out := make([]api.MeterStat, 0, len(t))
	for _, k := range t.Keys() {
		out = append(out, wireStat(k.Res, k.Op, t[k]))
	}
	return out
}

// wireLoad is the cost of every sample of totals, one entry per resource
// that has any.
func (s *Server) load() api.Load {
	reg := s.deps.Meter
	load := wireLoad(reg.Since(), reg.Totals())
	if s.deps.Fallbacks != nil {
		n := s.deps.Fallbacks()
		load.Fallbacks = &n
	}
	return load
}

func wireLoad(since time.Time, totals []meter.Total) api.Load {
	all := meter.Tally{}
	for _, tot := range totals {
		all = all.Merge(tot.Tally)
	}
	load := api.Load{Since: since, Stats: []api.MeterStat{}}
	for _, k := range all.Keys() {
		if s := all[k]; k.Res == meter.Workspace && s.Spawns > 0 {
			load.CLIOps = append(load.CLIOps, wireStat(k.Res, k.Op, s))
		}
	}
	for _, res := range meter.Resources {
		if s := all.Total(res); s.Calls > 0 || s.Points > 0 {
			load.Stats = append(load.Stats, wireStat(res, "", s))
		}
	}
	return load
}

// rollsUp says a scope's phases, named "<name>/<phase>", count in its total.
func rollsUp(name string) bool {
	return strings.HasPrefix(name, "job:") || strings.HasPrefix(name, "loop:")
}

func isPhaseOf(name, parent string) bool {
	return rollsUp(parent) && strings.HasPrefix(name, parent+"/")
}

// recentWindow is the span of the report's Recent and of the doctor's load
// rates: a rate since start-up is skewed by the cold sweep that fills the
// caches.
const recentWindow = 10 * time.Minute

// wireMeter is the report of a registry as of now.
func (s *Server) wireMeter(now time.Time) api.MeterReport {
	reg := s.deps.Meter
	rep := api.MeterReport{At: now, Load: api.Load{Stats: []api.MeterStat{}}, Scopes: []api.MeterScope{}}
	if reg == nil {
		return rep
	}
	rep.Since = reg.Since()
	rep.Load = s.load()
	rep.Scopes = wireScopes(reg.Totals(), true)
	since, recent := reg.Recent(recentWindow)
	rep.Recent = &api.MeterWindow{Since: since, Scopes: wireScopes(recent, false)}
	return rep
}

func wireScopes(totals []meter.Total, last bool) []api.MeterScope {
	out := []api.MeterScope{}
	for _, tot := range totals {
		stats := tot.Tally
		for _, other := range totals {
			if isPhaseOf(other.Name, tot.Name) {
				stats = stats.Merge(other.Tally)
			}
		}
		sc := api.MeterScope{Name: tot.Name, Runs: tot.Runs, WallMS: tot.Wall.Milliseconds(), Stats: wireTally(stats)}
		if last {
			sc.LastAt, sc.LastWall = tot.LastAt, tot.LastWall.Milliseconds()
			if tot.Last != nil {
				sc.Last = wireTally(tot.Last)
			}
		}
		out = append(out, sc)
	}
	return out
}

// loadChecks is one info line per resource with calls: its hourly rate over
// the recent window and the scopes that spent most of it.
func loadChecks(reg *meter.Registry, now time.Time) []api.DoctorCheck {
	if reg == nil {
		return nil
	}
	since, totals := reg.Recent(recentWindow)
	window := max(now.Sub(since), time.Minute).Round(time.Minute)
	hours := window.Hours()
	var out []api.DoctorCheck
	for _, res := range meter.Resources {
		type share struct {
			name  string
			calls int64
		}
		var all meter.Stat
		var shares []share
		for _, tot := range totals {
			s := tot.Tally.Total(res)
			all = all.Merge(s)
			if s.Calls > 0 {
				shares = append(shares, share{tot.Name, s.Calls})
			}
		}
		if all.Calls == 0 {
			continue
		}
		slices.SortFunc(shares, func(a, b share) int { return cmp.Or(cmp.Compare(b.calls, a.calls), strings.Compare(a.name, b.name)) })
		var top []string
		for _, sh := range shares[:min(topScopes, len(shares))] {
			top = append(top, fmt.Sprintf("%s %d%%", sh.name, (sh.calls*100+all.Calls/2)/all.Calls))
		}
		rate := func(n int64) int64 { return int64(float64(n)/hours + 0.5) }
		out = append(out, api.DoctorCheck{Mark: api.DoctorInfo, Name: "load: " + string(res),
			Info: fmt.Sprintf("%d/h, %d spawns/h, %d pts/h over the last %s; top: %s", rate(all.Calls), rate(all.Spawns), rate(all.Points),
				strings.TrimSuffix(window.String(), "0s"), strings.Join(top, ", "))})
	}
	return out
}
