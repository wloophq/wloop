# Timeline

The unit's events, newest first, and the Recent view's activity.

Grid of `time` (`code`, `muted`) and event: `kind` in 600 weight, `who` in `muted`, data in `code`. Each event carries its kind's served tone as a class (`loop`, `me` or `people`, from `timeline.toml` for the core's kinds and from a plugin's `ui.toml` for its own): the Recent view colors the kind with `loop-text`, `me-text` or `reviewer-text`, and its "Loop only" filter keeps the `loop` ones. Dashed `rule` between events. The Recent view uses the same grammar with sticky day overlines. A task's timeline folds its events into facts instead (see TimelineRow).
