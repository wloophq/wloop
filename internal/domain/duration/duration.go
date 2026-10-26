// Package duration is the one grammar of durations in manifests, the
// configuration, step arguments and the API: a Go duration or <n>d, positive.
package duration

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Day is what d means: 24 hours.
const Day = 24 * time.Hour

// Parse reads s, a Go duration such as 10m or 1h30m, or a whole number of
// days such as 7d; zero and negative durations are refused.
func Parse(s string) (time.Duration, error) {
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || n == "" || n[0] < '0' || n[0] > '9' {
			return 0, fmt.Errorf("%q is not a duration: a whole number of days is written like 7d", s)
		}
		d = time.Duration(days) * Day
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q is not a duration, such as 30s, 10m, 2h or 1d", s)
		}
	}
	if d <= 0 {
		return 0, errors.New("a duration must be longer than zero")
	}
	return d, nil
}

// Format writes d in whole days when it is a whole number of days, else as
// Go does without trailing zero units: 10m, never 10m0s.
func Format(d time.Duration) string {
	if d > 0 && d%Day == 0 {
		return strconv.FormatInt(int64(d/Day), 10) + "d"
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
