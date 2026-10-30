package slack

import (
	"fmt"
	"regexp"
	"strings"
)

// Slack ids: a user's starts with U or W, a user group's with S, each followed by at least
// eight letters or digits.
var (
	userIDRE  = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)
	groupIDRE = regexp.MustCompile(`^S[A-Z0-9]{8,}$`)
)

// IsUserID says whether s is a Slack user id.
func IsUserID(s string) bool { return userIDRE.MatchString(s) }

// IsGroupID says whether s is a Slack user group id.
func IsGroupID(s string) bool { return groupIDRE.MatchString(s) }

// Person keys the plugin writes to the ledger: a user's, a user group's and a team or role
// named in a message.
const (
	userKeyPrefix  = string(Provider) + ":"
	groupKeyPrefix = "group:"
)

// UserKey is the ledger key of user id.
func UserKey(id string) string { return userKeyPrefix + id }

// GroupKey is the ledger key of user group id.
func GroupKey(id string) string { return groupKeyPrefix + id }

// TeamKey is the ledger key of a team or role named name.
func TeamKey(name string) string { return groupKeyPrefix + name }

// ValidPersonKey says why key is not a person key the plugin writes: "slack:<user id>",
// "group:<user group id>" or "group:<name>", where a name is one line of text that is no id
// and carries no key prefix or mention of its own.
func ValidPersonKey(key string) error {
	if id, ok := strings.CutPrefix(key, userKeyPrefix); ok {
		if !IsUserID(id) {
			return fmt.Errorf("person key %q: not a user id", key)
		}
		return nil
	}
	name, ok := strings.CutPrefix(key, groupKeyPrefix)
	switch {
	case !ok:
		return fmt.Errorf("person key %q: neither a user nor a group", key)
	case IsGroupID(name):
		return nil
	case strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name:
		return fmt.Errorf("person key %q: a name has no surrounding space", key)
	case strings.ContainsAny(name, "<>\n"):
		return fmt.Errorf("person key %q: a name carries a mention", key)
	case strings.HasPrefix(name, groupKeyPrefix) || strings.HasPrefix(name, userKeyPrefix):
		return fmt.Errorf("person key %q: a name carries a key", key)
	case IsUserID(name) || teamNameIsID.MatchString(name):
		return fmt.Errorf("person key %q: a name is no id", key)
	}
	return nil
}

// teamNameIsID is a name that is a rendered user: "ana (U0000000001)" or a bare id.
var teamNameIsID = regexp.MustCompile(`\([UWS][A-Z0-9]{8,}\)$`)
