package task

import "time"

// AddWorkdays is t moved n working days (Monday to Friday) forward, at the same clock time in loc.
func AddWorkdays(t time.Time, n int, loc *time.Location) time.Time {
	t = t.In(loc)
	for n > 0 {
		t = t.AddDate(0, 0, 1)
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n--
		}
	}
	return t
}
