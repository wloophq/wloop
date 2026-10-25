## What to look for

Measure the change against the goal its description states, then check:

- **The goal.** Does the change do what it says, completely? A missing piece
  without which the goal is not met blocks.
- **Edges.** Empty and boundary inputs, error paths, retries, concurrency and
  ordering, data already stored in the old shape.
- **Removed behavior.** For each deleted or rewritten line, name what it used
  to guarantee (a guard, a validation, an error path) and find where the new
  code still guarantees it.
- **Contracts.** Callers of a changed function, type or endpoint: a new
  precondition, a different return shape, a new error, an ordering they
  relied on.
- **Language pitfalls** that fit the code at hand: nil or null handling,
  shared mutable state, closures capturing loop variables, time zones,
  floating point equality, injection in built queries or commands.
- **Tests.** Each changed behavior has a test that would fail without the
  change. Changed behavior with no such test blocks.
- **Docs.** Documentation of the changed code still says what the code does.
  A doc the change made wrong blocks.
- **Duplication.** The same rule, mapping or decision now lives in two
  places. Code that looks alike but encodes different decisions is fine.
- **Simplicity.** A simpler or well-known solution that holds for the
  actual constraints, named, with a reason it matters here.
- **Repository conventions** the contributors' and agents' instruction files
  state, the nearest one to the changed file winning.

## Risky surfaces

Schema migrations, data deletion or backfills, auth and permissions,
billing, and infrastructure. A review that would approve a change to one of
them asks the user first.

## Later rounds

New findings only on lines the new commits touched, unless a previous
finding was missed and blocks.

## What to leave out

- Problems on lines the change did not touch, unless they make the change
  wrong or keep it from its goal.
- What a linter, type checker or compiler reports on its own.
- Matters of taste with no consequence.
- Anything that restates what the change does or confirms that something
  works.

## Labels and what blocks

Label every finding with Conventional Comments: `issue`, `suggestion`,
`nitpick`, `question` or `note`, decorated `(blocking)` or
`(non-blocking)`. A finding blocks when the change is wrong, unsafe, breaks a
contract, loses data, or does not reach its goal without the fix; everything
else is non-blocking.

## The body

The verdict is the review's event, never text in the body. The body holds
only what has no line to attach to; when something blocks, its first line
names it in one sentence. Threads still open from earlier rounds are never
listed in it: GitHub shows them. A clean approval is the event alone, with
no body and no comments; a rule on how the body opens applies only when
there is a body. On a later round, the body carries new findings without a
line and, while blockers stand, one line naming them.

## Voice

Short and specific: the problem, its consequence, the fix. One finding per
comment, and a suggestion block when the fix is a direct replacement of the
commented lines. Write in the pull request's language. Before publishing,
reread every comment and the body against this voice and cut what fails it.
