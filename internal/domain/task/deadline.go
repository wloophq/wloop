package task

import "time"

// ParseDeadline reads an item's deadline: RFC 3339 as is, "YYYY-MM-DD" as 18:00 that day in loc, anything else none.
func ParseDeadline(s string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if d, err := time.ParseInLocation(time.DateOnly, s, loc); err == nil {
		return d.Add(18 * time.Hour), true
	}
	return time.Time{}, false
}
