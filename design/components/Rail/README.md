# Rail

The left column: the brand mark, the omnibox entry, the views with their counts by section, then whom the rows shown wait on.

- `rail-mark`: the product name (`mark`), from the brand.
- `find`: the omnibox entry, a hairline box in `faint` reading "Search or ask" with its `⌘K` key; today it opens the actions palette.
- `nav`: each view, a `label` and a `count` (`code`, `faint`), in a `radius-md` row. The current view is `hover` with `ink` text; a count that asks for attention past its threshold is `late`.
- `rail-head`: a section's head in `faint`, the provider's served name, above that plugin's views.
- `rail-empties`: the views that count nothing, but the one shown, fold into one Empty entry with their count; it expands in place (`aria-expanded`) and lists them.

The profile's views come first, in the order of its `views.toml`, under no head; each plugin's views follow under its name. The key that jumps to a view (`g i`) is the view's, and shows in the help and the palette with the view's label. Hover answers in `motion-fast`.

## Consumer provides
The catalog's views, already ordered, each with its id, label, key and count; the plugins' views with their provider and order, and each provider's name.

## Do / don't
- Don't name a view in the client: an added, renamed or removed view in the profile changes the rail with no client change.
- Don't fold the view shown, nor a view whose count is not known.
