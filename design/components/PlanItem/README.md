# PlanItem

One thing a workflow would do now on a task, and why.

When in `code` (`loop-text` while pending, `err` when blocked), the workflow name in 600 weight, the task's title and its unit in `muted`, `observe` in `late` for plan-only workflows, the reason in `body`, then the item's actions in the order the daemon serves them. A plan item that waits for your approval is a gate of kind `plan` on its task, and shows there as a GateCard.

## Consumer provides
The workflow item from the daemon's plan, with its task and the actions it offers.
