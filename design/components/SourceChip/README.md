# SourceChip

*Proposed.* Where an entry, draft or plan item comes from: provider mark, unit kind, unit label.

Built only from the generic card: the provider's two-letter mark (or the plugin's declared one), the plugin's unit kind, and `card.label` in `code`. Neutral on `hover`, no hue for any provider. Dashed with `.stale` when the plugin is off. Several units: the first chip, then `+n`. The `LI Issue` chip shows a plugin that does not exist yet rendering with no client change.

## Consumer provides
`provider`, `kind`, `label`, `link`, `stale`. Hover shows the unit's open items.
