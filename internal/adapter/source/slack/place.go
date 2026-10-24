package slack

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/placement"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
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
	Contexts     func() placement.Map
}

var (
	_ placement.Hints    = Places{}
	_ placement.Defaults = Places{}
)

func (p *plugin) places(cfg config.Config) Places {
	return Places{
		Hints:        extract.Store{DB: p.deps.DB.Extraction(), Segments: p.deps.DB.Segments()},
		Conversation: extractionFacts{p.src.store}.Conversation,
		Contexts:     cfg.PlacementMap,
	}
}

// Suggested is the rule the extraction named for item; for no item, the one
// rule every hinted item of the unit names.
func (p Places) Suggested(ctx context.Context, u unit.Unit, item string) (placement.Placement, bool, error) {
	hints, err := p.Hints.Hints(ctx, u)
	if err != nil {
		return placement.Placement{}, false, err
	}
	named := map[string]bool{}
	for id, h := range hints {
		if h.Context != "" && (item == "" || id == item) {
			named[h.Context] = true
		}
	}
	if len(named) != 1 {
		return placement.Placement{}, false, nil
	}
	match := slices.Collect(maps.Keys(named))[0]
	for _, r := range p.Contexts().Rules {
		if r.Match == match {
			return ruled(r), true, nil
		}
	}
	return placement.Placement{}, false, nil
}

// Default is the first "slack:#<name>" rule matching the unit's channel; a
// direct message has none.
func (p Places) Default(ctx context.Context, u unit.Unit, _ string) (placement.Placement, bool, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return placement.Placement{}, false, nil
	}
	c, err := p.Conversation(ctx, k.Conversation)
	switch {
	case errors.Is(err, dslack.ErrUnknownUnit):
		return placement.Placement{}, false, nil
	case err != nil:
		return placement.Placement{}, false, err
	case c.Name == "" || c.Kind.DM():
		return placement.Placement{}, false, nil
	}
	channel := unit.Unit{Provider: dslack.Provider, Key: "#" + c.Name}
	for _, r := range p.Contexts().Rules {
		if strings.HasPrefix(r.Match, channelRule) && r.Matches(channel) {
			return ruled(r), true, nil
		}
	}
	return placement.Placement{}, false, nil
}

func ruled(r placement.Rule) placement.Placement {
	return placement.Placement{Repo: r.Repo, Checkout: r.Checkout, Role: r.Role, Shared: r.Shared}
}
