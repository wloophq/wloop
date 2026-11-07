# TopBar

The cockpit's header and the list toolbar under each list's head.

## Header
- `search`: the filter (`/` focuses it); qualifiers like `where:`, `wait:me`.
- `sync`: freshness in `code` `muted`; `late` when slow or paused.
- `notice`: the last outcome, right-aligned, `muted` (or `err`). One line, ellipsis.

The brand mark lives at the top of the Rail. The pause control sits at the far right as a Button.

## List toolbar
One row of `pill`s (hairline, `radius-pill`, `muted` text, the value in `ink`):

- **Group** with the field it groups by, a menu of the fields the list offers, tag keys included.
- **Sort** with the field it sorts by and its direction arrow, a menu of the view's own order and every sortable field the list offers.
- one chip per filter, `sel` with a `me` border, reading the field and its values with a `×`; it says "Remove filter …" to a screen reader and takes that filter out.
- **Filter**, dashed, with its `+` key: a palette of the list's fields, then the values the list holds of the one picked; picking one adds it to the filter.
- the strip's legend, then, at the right, the count ("n of m"), Clear, the list/board switch (a `seg` with `aria-pressed`) and Copy list.

Hover answers in `motion-fast`.
