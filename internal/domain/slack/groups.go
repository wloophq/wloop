package slack

import (
	"regexp"
	"strings"
)

// UnknownGroup names a user group whose handle no message gave.
const UnknownGroup = "a user group"

var subteamMention = regexp.MustCompile(`<!subteam\^([A-Z0-9]+)(?:\|([^<>]*))?>`)

// GroupHandles adds to into the handle of each user group texts mention as
// <!subteam^ID|@handle>.
func GroupHandles(into map[string]string, texts ...string) {
	for _, t := range texts {
		for _, m := range subteamMention.FindAllStringSubmatch(t, -1) {
			if h := strings.TrimSpace(m[2]); h != "" {
				into[m[1]] = "@" + strings.TrimPrefix(h, "@")
			}
		}
	}
}

// GroupName is how a person reads user group id: its handle, else UnknownGroup.
func GroupName(id string, handles map[string]string) string {
	if h := handles[id]; h != "" {
		return h
	}
	return UnknownGroup
}
