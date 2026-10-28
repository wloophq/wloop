package slack

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Segments is the DM segment map in the store, as units.
type Segments struct {
	Store *store.SegmentStore
	Clock Clock
}

var (
	_ dslack.Segments      = Segments{}
	_ dslack.SegmentWriter = Segments{}
)

// slackTS is the width Slack ts have from 2001 to 2286; the store compares ts as text.
var slackTS = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)

func checkTS(ts string) error {
	if !slackTS.MatchString(ts) {
		return fmt.Errorf("ts %q is not a Slack ts", ts)
	}
	return nil
}

func segmentUnit(team, conv, id string) unit.Unit {
	return dslack.Key{Team: team, Conversation: conv, Segment: id}.Unit()
}

func (s Segments) UnitsFor(ctx context.Context, conv, ts string) ([]unit.Unit, error) {
	if err := checkTS(ts); err != nil {
		return nil, err
	}
	rows, err := s.Store.Covering(ctx, dslack.Provider, conv, ts)
	if err != nil {
		return nil, err
	}
	out := make([]unit.Unit, len(rows))
	for i, r := range rows {
		out[i] = segmentUnit(r.Team, r.Conversation, r.ID)
	}
	return out, nil
}

func (s Segments) Range(ctx context.Context, u unit.Unit) (conv, fromTS, toTS string, err error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return "", "", "", err
	}
	if k.Segment == "" {
		return "", "", "", fmt.Errorf("%s is not a DM segment", u)
	}
	r, err := s.Store.Segment(ctx, dslack.Provider, k.Conversation, k.Segment)
	switch {
	case errors.Is(err, store.ErrUnknownSegment):
		return "", "", "", fmt.Errorf("%w: %s", dslack.ErrUnknownUnit, u)
	case err != nil:
		return "", "", "", fmt.Errorf("range of %s: %w", u, err)
	case r.Team != k.Team:
		return "", "", "", fmt.Errorf("%w: %s", dslack.ErrUnknownUnit, u)
	}
	return r.Conversation, r.FromTS, r.ToTS, nil
}

func (s Segments) Apply(ctx context.Context, team, conv string, spans []dslack.Span) ([]unit.Unit, error) {
	rows := make([]store.SegmentSpan, len(spans))
	for i, sp := range spans {
		if err := validSpan(team, conv, sp); err != nil {
			return nil, fmt.Errorf("span %d of %s: %w", i, conv, err)
		}
		rows[i] = store.SegmentSpan(sp)
	}
	got, err := s.Store.Apply(ctx, dslack.Provider, team, conv, rows, segmentUnit, s.Clock.Now())
	if err != nil {
		return nil, err
	}
	out := make([]unit.Unit, len(got))
	for i, r := range got {
		out[i] = segmentUnit(r.Team, r.Conversation, r.ID)
	}
	return out, nil
}

func validSpan(team, conv string, sp dslack.Span) error {
	check := func(k dslack.Key) error { return dslack.ValidKey(k.Unit().Key) }
	if sp.ID != "" {
		if err := check(dslack.Key{Team: team, Conversation: conv, Segment: sp.ID}); err != nil {
			return err
		}
	}
	if err := check(dslack.Key{Team: team, Conversation: conv, TS: "0000000000.000000"}); err != nil {
		return err
	}
	if err := checkTS(sp.FromTS); err != nil {
		return err
	}
	if sp.ToTS != "" {
		return checkTS(sp.ToTS)
	}
	return nil
}
