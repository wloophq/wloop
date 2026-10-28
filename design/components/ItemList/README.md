# ItemList

*Proposed.* The asks inside a task's units, in the inspector.

Each line: kind glyph (`→` request, `?` question, `◇` decision, `□` todo), text, status, and a second line with who asked whom and since when. Resolved items strike through in `muted` with a `loop-text` check. A confirmed item carries the `confirmed` flag.

## Consumer provides
The items (`kind`, `summary`, `state`, `waiting_on`, `since`) the plugin derived or extracted.
