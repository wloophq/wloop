package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a five-field schedule ("0 9 * * 1-5") read in the location of the
// time it is asked about. When both day fields are restricted, a day matches
// either, as in cron(8).
type Cron struct {
	minute, hour, dom, month, dow uint64
	domAny, dowAny                bool
}

type cronField struct {
	name     string
	min, max int
}

var cronFields = [5]cronField{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

// cronHorizon bounds the search for a firing; a schedule with none in it never fires.
const cronHorizon = 8 * 366 * 24 * time.Hour

func ParseCron(src string) (*Cron, error) {
	parts := strings.Fields(src)
	if len(parts) != len(cronFields) {
		return nil, fmt.Errorf("schedule %q: want 5 fields (minute hour day-of-month month day-of-week), got %d", src, len(parts))
	}
	var sets [5]uint64
	for i, p := range parts {
		s, err := parseCronField(p, cronFields[i])
		if err != nil {
			return nil, err
		}
		sets[i] = s
	}
	if sets[4]&(1<<7) != 0 {
		sets[4] |= 1
	}
	return &Cron{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4], domAny: parts[2] == "*", dowAny: parts[4] == "*"}, nil
}

func parseCronField(src string, f cronField) (uint64, error) {
	var set uint64
	for _, term := range strings.Split(src, ",") {
		rng, stepSrc, stepped := strings.Cut(term, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepSrc)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s step %q must be a positive number", f.name, stepSrc)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if rng != "*" {
			loSrc, hiSrc, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = cronValue(loSrc, f); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = cronValue(hiSrc, f); err != nil {
					return 0, err
				}
			} else if stepped {
				hi = f.max
			}
			if hi < lo {
				return 0, fmt.Errorf("%s range %s ends before it starts", f.name, rng)
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func cronValue(src string, f cronField) (int, error) {
	v, err := strconv.Atoi(src)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", f.name, src)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%s %d out of range %d-%d", f.name, v, f.min, f.max)
	}
	return v, nil
}

func (c *Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<t.Day()) != 0
	dow := c.dow&(1<<int(t.Weekday())) != 0
	if c.domAny || c.dowAny {
		return dom && dow
	}
	return dom || dow
}

// Next is the first firing strictly after after; zero when none comes within years.
func (c *Cron) Next(after time.Time) time.Time {
	loc := after.Location()
	t := time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), after.Minute(), 0, 0, loc).Add(time.Minute)
	for limit := after.Add(cronHorizon); t.Before(limit); {
		switch {
		case c.month&(1<<int(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case c.hour&(1<<t.Hour()) == 0:
			// Stepping in absolute time keeps a repeated hour of a fall-back from
			// rebuilding the same wall-clock instant forever.
			t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
		case c.minute&(1<<t.Minute()) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// Prev is the last firing at or before at; zero when none came within years.
func (c *Cron) Prev(at time.Time) time.Time {
	loc := at.Location()
	t := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	for limit := at.Add(-cronHorizon); t.After(limit); {
		switch {
		case c.month&(1<<int(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).Add(-time.Minute)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Add(-time.Minute)
		case c.hour&(1<<t.Hour()) == 0:
			t = t.Add(-time.Duration(t.Minute()+1) * time.Minute)
		case c.minute&(1<<t.Minute()) == 0:
			t = t.Add(-time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
