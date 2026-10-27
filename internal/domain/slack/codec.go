package slack

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Key is a Slack unit key: a thread when TS is set, a DM segment when Segment is.
type Key struct{ Team, Conversation, TS, Segment string }

func (k Key) Unit() unit.Unit {
	if k.Segment != "" {
		return unit.Unit{Provider: Provider, Key: k.Team + "/" + k.Conversation + "/seg/" + k.Segment}
	}
	return unit.Unit{Provider: Provider, Key: k.Team + "/" + k.Conversation + "/" + k.TS}
}

// ParseUnit refuses a unit of another provider or an invalid key.
func ParseUnit(u unit.Unit) (Key, error) {
	if u.Provider != Provider {
		return Key{}, fmt.Errorf("not a slack unit: %s", u)
	}
	return parseKey(u.Key)
}

var (
	teamRE    = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)
	convRE    = regexp.MustCompile(`^[CDG][A-Z0-9]+$`)
	tsRE      = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	segmentRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func parseKey(s string) (Key, error) {
	p := strings.Split(s, "/")
	bad := fmt.Errorf("invalid slack key %q", s)
	if len(p) < 3 || !teamRE.MatchString(p[0]) || !convRE.MatchString(p[1]) {
		return Key{}, bad
	}
	switch {
	case len(p) == 3 && tsRE.MatchString(p[2]):
		return Key{Team: p[0], Conversation: p[1], TS: p[2]}, nil
	case len(p) == 4 && p[2] == "seg" && segmentRE.MatchString(p[3]):
		return Key{Team: p[0], Conversation: p[1], Segment: p[3]}, nil
	}
	return Key{}, bad
}

type codec struct{}

func (codec) Provider() unit.ProviderID { return Provider }

// Target is how a command line names a thread: a message's ref
// (<conversation>/<ts>) or permalink, or its key.
func (codec) Target() string { return "<ref|permalink|" + string(Provider) + ":key>" }

func (codec) Valid(key string) error {
	_, err := parseKey(key)
	return err
}
