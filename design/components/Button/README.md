# Button

The `.act` button runs a daemon action or a local command; one per verb.

## Variants
- **default**: `panel` fill, `rule` border, `meta` size, `radius-md`.
- **primary**: `me` fill, `on-me` text. The daemon's recommended action, at most one per region (row, detail, question).
- **marked default** (`.default`): `me` border and `me-text` text. The default of a pair of alternatives in a region whose primary is another action.
- **busy**: `disabled`, 50% opacity, progress cursor, while the request is in flight.
- **off**: `disabled` with `.off`, dashed border and `muted` text, when the action does not apply now; its `title` says why.
- **toggle**: a `.toggle` group of buttons with `aria-pressed`; pressed is `sel` with a `me` border.

## Consumer provides
The label (a verb, sentence case), a `title` that states the consequence, and `data-focus` for keyboard return.

## Do / don't
- Do put the recommended action first.
- Don't put more than one action button in a table row; the rest live in the palette and the detail panel.
