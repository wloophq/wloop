// Package workday counts time in working days, Monday to Friday.
package workday

import "time"

// Between counts the time between from and to, leaving out Saturdays
// and Sundays. Both instants are taken in UTC so the day boundaries agree.
func Between(from, to time.Time) time.Duration {
	from, to = from.UTC(), to.UTC()
	if !to.After(from) {
		return 0
	}
	var total time.Duration
	cur := from
	for cur.Before(to) {
		next := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, time.UTC)
		if next.After(to) {
			next = to
		}
		if wd := cur.Weekday(); wd != time.Saturday && wd != time.Sunday {
			total += next.Sub(cur)
		}
		cur = next
	}
	return total
}

// Add is the earliest instant to with Between(from, to) at least d: from
// plus d counted in working days. A d of zero or less is from.
func Add(from time.Time, d time.Duration) time.Time {
	cur := from.UTC()
	for d > 0 {
		next := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, time.UTC)
		if wd := cur.Weekday(); wd != time.Saturday && wd != time.Sunday {
			span := next.Sub(cur)
			if span >= d {
				return cur.Add(d)
			}
			d -= span
		}
		cur = next
	}
	return cur
}
