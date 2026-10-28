package slack

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// channelRule is the prefix of a contexts rule that places a channel by its name.
const channelRule = string(dslack.Provider) + ":#"

// Places are Slack's placement hints and defaults. The hint is the contexts
// rule the extraction suggested for the unit's items; the default is the
// first "slack:#<channel name>" rule that matches the unit's channel.
type Places struct {
	Hints interface {
		Hints(ctx context.Context, u unit.Unit) (map[string]extract.Hint, error)
	}
	// Conversation fails with dslack.ErrUnknownUnit for a conversation not stored.
	Conversation func(ctx context.Context, id string) (dslack.Conversation, error)
	Contexts     func() []config.PlacementRule
}

// Placement is where u's work runs: the plugin's default and the
// extraction's suggestion for item.
func (p Places) Placement(ctx context.Context, u unit.Unit, item string) (src.Placements, error) {
	var out src.Placements
	if d, ok, err := p.Default(ctx, u, item); err != nil {
		return out, err
	} else if ok {
		out.Default = &d
	}
	if s, ok, err := p.Suggested(ctx, u, item); err != nil {
		return out, err
	} else if ok {
		out.Suggested = &s
	}
	return out, nil
}

// Suggested is the rule the extraction named for item; for no item, the one
// rule every hinted item of the unit names.
func (p Places) Suggested(ctx context.Context, u unit.Unit, item string) (src.Placement, bool, error) {
	hints, err := p.Hints.Hints(ctx, u)
	if err != nil {
		return src.Placement{}, false, err
	}
	named := map[string]bool{}
	for id, h := range hints {
		if h.Context != "" && (item == "" || id == item) {
			named[h.Context] = true
		}
	}
	if len(named) != 1 {
		return src.Placement{}, false, nil
	}
	match := slices.Collect(maps.Keys(named))[0]
	for _, r := range p.Contexts() {
		if r.Match == match {
			return ruled(r), true, nil
		}
	}
	return src.Placement{}, false, nil
}

// Default is the first "slack:#<name>" rule matching the unit's channel; a
// direct message has none.
func (p Places) Default(ctx context.Context, u unit.Unit, _ string) (src.Placement, bool, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return src.Placement{}, false, nil
	}
	c, err := p.Conversation(ctx, k.Conversation)
	switch {
	case errors.Is(err, dslack.ErrUnknownUnit):
		return src.Placement{}, false, nil
	case err != nil:
		return src.Placement{}, false, err
	case c.Name == "" || c.Kind.DM():
		return src.Placement{}, false, nil
	}
	channel := unit.Unit{Provider: dslack.Provider, Key: "#" + c.Name}
	for _, r := range p.Contexts() {
		if strings.HasPrefix(r.Match, channelRule) && glob(r.Match, channel.String()) {
			return ruled(r), true, nil
		}
	}
	return src.Placement{}, false, nil
}

func ruled(r config.PlacementRule) src.Placement {
	return src.Placement{Repo: r.Repo, Checkout: r.Checkout, Role: r.Role, Shared: r.Shared}
}

// glob says whether pattern, with * and ?, matches s.
func glob(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti := 0, 0
	star, mark := -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// places are the plugin's placement over its store and the host's rules.
func (p *plugin) places() Places {
	return Places{
		Hints:        extract.Store{DB: p.db.Extraction(), Segments: p.db.Segments()},
		Conversation: extractionFacts{p.src.store}.Conversation,
		Contexts:     func() []config.PlacementRule { return p.all.Placement },
	}
}
