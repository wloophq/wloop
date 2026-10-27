---
title: Workspaces and board columns
description: The two kinds of workspace, how a pull request enters and leaves the loop, and how its card moves on the Orca board.
---

The loop acts where a local workspace exists, in Claude Desktop or in Orca. A workspace is one of two kinds, told apart by its display name:

- **Review workspace**, named `REVIEW <branch>` (`github.review_prefix`): someone else's pull request that you review.
- **Author workspace**, any other name: a pull request you develop, whether you opened it or took it over.

Renaming a review workspace in Orca turns it into an author workspace.

## Entering and leaving

| How | Becomes |
|---|---|
| your review is requested by name, and the first round runs | review |
| `{{command}} review <url>` | review |
| a workspace linked to your pull request, or on its head branch | author |
| `{{command}} take-over <url>` | author; an existing review workspace is renamed to the branch name |
| `{{command}} remove <url>`, or Remove in the cockpit | leaves the loop |

`remove` deletes the workspace when its checkout holds nothing that is not on GitHub, and refuses otherwise with the list of what is at stake; `--force` deletes it with what it holds, for good. Deleting a review workspace by hand only frees its checkout: the pull request stays followed (see [workflows](/concepts/workflows/#automatic-rounds)). The cockpit's Done view lists the workspaces of merged or closed pull requests, with Remove on each and "Remove all clean" for the lot.

## Who moves cards

Only Orca has a board, so cards and columns exist for Orca workspaces alone. A Claude Desktop workspace shows its state in the cockpit, the terminal client and the menu bar.

{{name}}'s store is the truth; the board shows it. {{name}} writes a card's column, comment and unread mark on every change to the turn and re-asserts them on every fast job. It never reads a column or comment back as state, and the agent never moves a card.

A card you drag outside the columns below is left there (its comment and unread mark still follow) until you move it back or a new round starts.

## Review workspace columns

The card follows the newest review turn of the pull request. A nudge never moves it.

| Newest review turn | Column |
|---|---|
| `sent`, `started` | `reviewing` (or `in-review` when the board has no `reviewing` column) |
| `asked`, `failed`, `unrecorded` | `needs-me`, marked unread, with the comment `asks: <summary>` or `stopped: <summary>` |
| `done` with an approval | `completed` |
| `done` with another verdict, `settled`, `dismissed` | `reviewed` |

The pull request merged or closed moves the card to `completed`. An approved card of an open pull request stays followed: new commits or a re-request bring it back to `reviewing`.

## Author workspace columns

| Local | GitHub | Column |
|---|---|---|
| the agent is working | any | `in-progress` |
| the agent stopped with the head pushed | pending | `in-review` |
| in sync | approved, checks green or absent, not a draft | `ready-to-merge` |
| any | changes requested on the current head | `in-progress`, marked unread |
| unpushed commits, card in `ready-to-merge` | any | `in-progress` |
| the newest address turn is `asked`, `failed` or `unrecorded` | any | `needs-me`, marked unread |
| | merged or closed | `completed` |

"Approved" also covers repositories without required reviews where someone approved; "green" also covers repositories without checks.

## Column names

`views.orca.columns` maps each state to a column of your board. A state left out keeps its default:

```toml
[views.orca.columns]
working = "reviewing"
working-fallback = "in-review"
needs-me = "needs-me"
reviewed = "reviewed"
ready = "ready-to-merge"
```

Orca has no read-only way to list a board's columns, so a missing column shows up only when a write fails; the daemon's log names it when a turn starts.

Orca's own status hooks (`orca-workspace-status.sh`, in Claude Code's settings) move cards to `in-progress` on every prompt. `doctor` reports them as competing with {{name}}'s writes. With the `in-review` fallback column they move review cards too.
