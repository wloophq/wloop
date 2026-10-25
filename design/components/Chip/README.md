# Chip

A pill-shaped toggle for a filter, a lens or a saved query.

`radius-pill`, `pill`-sized `sans` text, `panel` fill, `rule` border, at most 32ch then ellipsis. Pressed (`aria-pressed="true"` or `.on`) is `sel` with a `me` border; `.on` chips that clear a lens end in `×`. `.off` is dashed at 60% opacity for an excluded term.

## Consumer provides
The label, `aria-pressed`, and the filter term it toggles.

## Do / don't
- Do use chips only for filters and lenses; actions are Buttons.
