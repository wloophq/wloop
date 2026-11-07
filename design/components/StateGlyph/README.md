# StateGlyph

The leading mark of a task row: the category of the task's state, by shape, after OmniFocus's status circle.

The profile names the states and gives each a category; the glyph draws the category, so a state a profile adds needs no new glyph. A `span.glyph` at `state-glyph` size whose `data-category` picks the shape, drawn in CSS in the waiter's hue (`w-*` on the row sets it):

| Category | Shape |
|---|---|
| `inbox` | dashed circle |
| `ready` | circle |
| `active` | circle with a centre dot |
| `waiting` | half-filled circle |
| `deferred` | dotted circle, faded |
| `done` | filled disc with a check |
| `dropped` | slashed circle |

The state's own label (Next, Someday, Reference) is the glyph's `title` and `aria-label`.

## Consumer provides
The task's state and its category from the catalog's states; when the lens offers the state's facet, the glyph is a facet button that filters the list by that state.

## Do / don't
- Do keep shape as the only cue of the category; color belongs to waits-on.
- Don't draw a glyph per state name or per kind of task: two states of one category share their glyph.
- Don't use the glyph on unit rows: units have no task state.
