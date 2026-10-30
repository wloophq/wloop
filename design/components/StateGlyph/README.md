# StateGlyph

*Proposed.* The leading mark of a task row: the category of the task's state, by shape, after OmniFocus's status circle.

The profile names the states and gives each a category; the glyph draws the category, so a state a profile adds needs no new glyph. One class per category, at `state-glyph` size:

| Category | Class | Shape |
|---|---|---|
| `inbox` | `sg-inbox` | dashed circle |
| `ready` | `sg-ready` | circle |
| `active` | `sg-active` | circle with a centre dot |
| `waiting` | `sg-waiting` | half-filled in the waiter's text hue (set `w-*` on the row) |
| `deferred` | `sg-deferred` | dotted circle, faded |
| `done` | `sg-done` | `loop` disc with a check |
| `dropped` | `sg-dropped` | slashed circle |

The state's own label (Next, Someday, Reference) is the glyph's `aria-label` and the word of the state pill beside the title.

## Consumer provides
The task's state and its category from the catalog's states, and the click: complete the task, running the profile's action with verb `done` when one exists.

## Do / don't
- Do keep shape as the only cue of the category; color belongs to waits-on.
- Don't draw a glyph per state name or per kind of task: two states of one category share their glyph.
- Don't use the glyph on unit rows: units have no task state.
