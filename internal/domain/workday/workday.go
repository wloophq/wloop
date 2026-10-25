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
