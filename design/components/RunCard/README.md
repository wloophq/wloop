# RunCard

One run of a task: a turn of an intent, where it ran and its session, with the three run actions.

An `article.d-run` on `bg`: the intent in 600 weight, its state as a `pill`, the workspace manager, and the session id in `code` with its state; the worktree in `code`; the resume command in `code` when it has one; then Open workspace, Enter session and Transcript. Transcript unfolds the run's lines in a `transcript` list under the card, in `meta`.

## Consumer provides
The run as the daemon serves it: its sequence, intent, state, manager, worktree, session and session state, resume command, and the actions it offers.

## Do / don't
- Do show all three actions on every run; one the run does not offer is `off` (dashed, `muted`, not allowed), so the card keeps its shape.
- Don't color the run's state: the pill is neutral, the waits-on strip of the task says whom it waits on.
