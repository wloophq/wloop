---
title: Profile format
description: The path grammar of profile blocks, their order, front matter and limits.
---

A profile is a directory. Its blocks are Markdown files whose path, relative to the profile, holds the conditions under which each applies. {{name}}'s bundled guides are the layer below your profile and follow the same grammar.

## The path

```text
<dimension>=<value>/ ... tags/<key>=<value>/ ... intent=<intent>.md
```

The leaf is always `intent=<intent>.md`, for `review`, `address`, `nudge` or any intent of yours whose manifest names a `profile`. Conditions come first, in this order:

| Rank | Dimension | Matches |
|---|---|---|
| 1 | `organization` | the repository's owner |
| 2 | `repo` | the repository name, only right after `organization` |
| 3 | `user` | the pull request's author |
| 4 | `team` | a team the review was requested through, as `<org>-<slug>` |
| 5 | `association` | GitHub's author association, lower-case with `-`, such as `first-time-contributor` |
| 6 | `label` | a label on the pull request |
| 7 | `base` | the base branch |
| 8 | `round` | `first` or `follow-up`, for intents whose manifest lists it in `dims` (review) |
| 9 to 11 | `kind`, `size`, `risk` | the brief's values |
| 12 | `tags/<key>=<value>` | a tag of the brief, sorted by key |

Values are compared without case and written lower-case; `/` and `%` in a value are written `%2F` and `%25`. A block with a brief condition, a tag or `paths` needs the brief: a turn without one leaves it out and lists it as skipped.

Files outside the condition directories that are not leaves (a README, notes) are ignored. Inside them, every `.md` file must be a leaf, so a file a block includes there is a `.txt`, or lives in a directory with no condition.

## Order

Every block that matches goes into the turn's `## Judgment` section, depth first through the paths: at each directory its own blocks ({{name}}'s before yours), then its subdirectories by rank and value, tags last. Each block gets a heading: `{{name}}'s review guide`, `Your review guide`, `Organization acme`, then `Repository api` one level down, and so on. Headings inside a block are shifted below its own. Where two blocks disagree, the later wins.

## Front matter

TOML between `+++` lines:

```markdown
+++
override = true
title = "Acme's API rules"
include = ["../docs/policy.md"]
paths = ["src/**/*.go"]

[delegates]
claude = ["/code-review high"]
codex = []
+++
```

| Key | Means |
|---|---|
| `override` | `true` drops the blocks before it whose path is an ancestor of its own; the root blocks, {{name}}'s included, are ancestors of every block |
| `title` | replaces the heading |
| `include` | inlines up to eight `.md` or `.txt` files of the profile, paths relative to the block |
| `paths` | the block applies only when a changed file matches one of these globs |
| `delegates` | per agent, skills the finding step runs besides the procedure's own work; they come from the last block that names them |

A file that opens with `---` fails with `front matter is TOML between +++ lines`. Any other key, a bad value or a path off the grammar stops every round, as a profile directory that does not exist does, with the file and the fix.

## Limits

A block is at most 16 KiB with its includes. A turn's judgment over 48 KiB drops blocks from the end of the order, never a root block, and the turn's sources list each one dropped.

## Commands

```sh
{{command}} profile where <intent> [conditions]
{{command}} profile add <intent>[,<intent>...] [conditions] [--section <h>] [--title <t>] [--override] [--paths <glob>]... [--apply] [-- <text>]
{{command}} profile check [<dir>]
{{command}} profile show <owner/repo|url> [--kind <intent>] [--show]
```

The conditions are `--org`, `--repo`, `--user`, `--team`, `--association`, `--label`, `--base`, `--round`, `--kind`, `--size`, `--risk` and `--tag <key>=<value>` (repeatable). `profile add` writes a tag in its one spelling (`lang=Golang` as `lang=go`).
