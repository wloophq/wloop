# StateGlyph

*Proposed.* The leading mark of a GTD row: the entry's state by shape, after OmniFocus's status circle.

One class per state (`sg-inbox`, `sg-next`, `sg-waiting`, `sg-scheduled`, `sg-someday`, `sg-reference`, `sg-done`, `sg-trash`) and per pre-commitment kind (`sg-message`, `sg-draft`, `sg-project`), at `state-glyph` size. Waiting fills half in the waiter's text hue (set `w-*` on the row). Done is a `loop` disc with a check.

## Consumer provides
The state or kind, an `aria-label` naming it, and the click: complete the entry, running the profile's action with verb `done` when one exists.

## Do / don't
- Do keep shape as the only state cue; color belongs to waits-on.
- Don't use the glyph on unit rows: units have no GTD state.
