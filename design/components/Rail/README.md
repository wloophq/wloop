# Rail

The left column of views with counts, in groups headed by an `overline`.

Each item is a link with a `label` and a `count` (`code`, `muted`). The current view is `sel` with a `strip-card` bar in `me` and 600 weight; a count that asks for attention past its threshold is `late` and bold. Below `breakpoint-narrow` the rail becomes a wrapped tab row with the bar on the bottom.

The profile's views come first (`core`), in the order of its `views.toml`; each plugin's views follow (`plugin`), under a hairline. The key that jumps to a view (`g i`) is the view's, and shows in the help and the palette with the view's label.

## Consumer provides
The catalog's views, already ordered, each with its id, label, key and whether the badge counts it; the plugins' views with their provider and order.

## Do / don't
- Don't name a view in the client: an added, renamed or removed view in the profile changes the rail with no client change.
- Don't add per-person lists here; "waiting on" is a view's grouping.
