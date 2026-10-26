package send

import (
	"net/url"
	"regexp"
	"strings"
)

type permalink struct{ channel, ts, threadTS string }

var (
	permalinkPathRE = regexp.MustCompile(`^/archives/([CGD][A-Z0-9]+)/p(\d{10})(\d{6})/?$`)
	tsRE            = regexp.MustCompile(`^\d{10}\.\d{6}$`)
)

func parsePermalink(s string) (permalink, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return permalink{}, false
	}
	if host := strings.ToLower(u.Hostname()); host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return permalink{}, false
	}
	m := permalinkPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return permalink{}, false
	}
	link := permalink{channel: m[1], ts: m[2] + "." + m[3]}
	if t := u.Query().Get("thread_ts"); tsRE.MatchString(t) {
		link.threadTS = t
	}
	return link, true
}

// IsPermalink says whether s is a Slack message permalink.
func IsPermalink(s string) bool {
	_, ok := parsePermalink(strings.TrimSpace(s))
	return ok
}
