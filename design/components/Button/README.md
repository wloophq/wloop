# Button

The `.act` button runs a daemon action or a local command; one per verb.

## Variants
- **default**: `panel` fill, `rule` border, `meta` size, `radius-md`.
- **primary**: `me` fill, `on-me` text. The daemon's recommended action, at most one per region (row, detail, question).
- **marked default** (`.default`): `me` border and `me-text` text. The default of a pair of alternatives in a region whose primary is another action.
- **busy**: `disabled` and `aria-busy`, 50% opacity, progress cursor, while the request is in flight. Every button with a `data-focus` and a click is busy from its click until its request answers, and so is the button each redraw makes under the same `data-focus`; a second click meanwhile does nothing.
- **result**: what an action answered shows under its region's buttons as `.act-result` (`meta` size, `muted`; `err` when refused), never only at the top of the page. A refusal of a draft's approval as stale also brings the card back with its stale note and its approval off.
- **off**: `disabled` with `.off`, dashed border and `muted` text, when the action does not apply now; its `title` says why.
- **segmented**: a `.seg` group of buttons in one hairline box, with `aria-pressed`; pressed is `raise` with `ink` text.

## Consumer provides
The label (a verb, sentence case), a `title` that states the consequence, and `data-focus` for keyboard return.

## Do / don't
- Do put the recommended action first.
- Don't put more than one action button in a table row; the rest live in the palette and the detail panel.
