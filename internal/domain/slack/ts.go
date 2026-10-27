package slack

import (
	"strconv"
	"strings"
	"time"
)

// TSTime is the instant a Slack ts names, to the microsecond; zero for a ts
// that names none.
func TSTime(ts string) time.Time {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	frac = (frac + "000000")[:6]
	us, _ := strconv.ParseInt(frac, 10, 64)
	return time.Unix(s, us*1000).UTC()
}
