package source

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// replaceLinks replaces the facts links of each reported unit with the
// addresses its snapshot links to that a codec reads; a gone unit links
// nothing.
func (b *Broker) replaceLinks(ctx context.Context, snaps map[unit.Unit]src.Snapshot) error {
	if b.Links == nil || len(snaps) == 0 {
		return nil
	}
	from := make([]unit.Unit, 0, len(snaps))
	links := []link.Link{}
	for u, s := range snaps {
		from = append(from, u)
		if s.Gone {
			continue
		}
		for _, l := range s.Links {
			to, ok := b.Units.Codecs.ParseURL(l.URL)
			if !ok || to == u {
				continue
			}
			kind := link.Kind(l.Kind)
			if !kind.Valid() {
				kind = link.Mention
			}
			ev := unit.Anchor{Unit: u, Ref: l.Evidence.Ref, Permalink: l.Evidence.Permalink, At: l.Evidence.At}
			if w := l.Evidence.Unit; w != (plugin.Unit{}) {
				if eu, err := b.Units.Of(w); err == nil {
					ev.Unit = eu
				}
			}
			links = append(links, link.Link{From: u, To: to, Kind: kind, Item: l.Item, Evidence: ev, Origin: link.Facts, At: s.At})
		}
	}
	slices.SortFunc(from, compareUnits)
	return b.Links.ReplaceFacts(ctx, from, links, b.Hosted.now())
}
