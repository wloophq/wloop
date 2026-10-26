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

| Rank | Dimension | Fact | Matches |
|---|---|---|---|
| 1 | `organization` | `pr.owner` | the repository's owner |
| 2 | `repo` | `pr.repo_name` | the repository name without its owner, only right after `organization` |
| 3 | `user` | `pr.author` | the pull request's author |
| 4 | `team` | `pr.requested_teams` | a team the review was requested through, as `<org>-<slug>` |
| 5 | `association` | `pr.association` | GitHub's author association, lower-case with `-`, such as `first-time-contributor` |
| 6 | `label` | `pr.labels` | a label on the pull request |
| 7 | `base` | `pr.base` | the base branch |
| 8 | `stage` | `turn.stage` | `first` or `follow-up`, for intents whose manifest lists it in `dims` (review) |
| 9 | `reviewer` | `pr.address.reviewers` | a reviewer the round answers, for intents whose manifest lists it in `dims` (address); a bot matches with or without `[bot]` |
| 10 to 12 | `kind`, `size`, `risk` | `brief.kind`, `brief.size`, `brief.risk` | the brief's values; `kind` takes GitHub's taxonomy under GitHub intents and any value under others |
| 13 | any other fact flagged as a dimension, by its name, then each enum field of the brief intent's `extra`, by its name, such as `approach=rewrite` | the fact, or `brief.extra.<name>` | the fact's value, or one of a list fact's values; sorted by name |
| 14 | `tags/<key>=<value>` | | a tag of the brief, sorted by key |

Each dimension reads a fact (see [Facts](/reference/facts/)). The first twelve are the names of today's paths; any other fact `builtin.Facts` flags as a dimension is a condition under its own name. A dimension whose fact belongs to a provider, such as every `pr.*` fact to GitHub, applies only to blocks of intents of that provider: `organization=acme/intent=triage.md` fails to load when `triage` is a Slack intent. A field of the brief intent's `extra` schema is a dimension when it is a string or an array with an `enum`, under the intents of the providers that brief intent serves; a field that shares a dimension's or a tag key's name is not one.

Values are compared without case and written lower-case; `/` and `%` in a value are written `%2F` and `%25`. A block with a brief condition (`kind`, `size`, `risk`, an extra field), a tag or `paths` needs the brief: a turn without one leaves it out and lists it as skipped.

Files outside the condition directories that are not leaves (a README, notes) are ignored. Inside them, every `.md` file must be a leaf, so a file a block includes there is a `.txt`, or lives in a directory with no condition.

## Order

Every block that matches goes into the turn's `## Judgment` section, depth first through the paths: at each directory its own blocks ({{name}}'s before yours), then its subdirectories by rank and value, tags last. Each block gets a heading: `{{name}}'s review guide`, `Your review guide`, `Organization acme`, then `Repository api` one level down, and so on. The headings of conditions come from the profile's `labels.toml`, below. Headings inside a block are shifted below its own. Where two blocks disagree, the later wins.

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

A file that opens with `---` fails with `front matter is TOML between +++ lines`. Any other key, a bad value or a path off the grammar stops every round, as a profile directory that does not exist does, with the file, the key and a close key when one exists. The [profile language](/reference/language/) lists the keys.

## Headings

`labels.toml` at the root of the profile names the heading of each condition. The bundled one writes today's English headings; a `labels.toml` of yours replaces the keys it sets and keeps the rest:

```toml
# Any dimension [dims] does not name, and a tag.
other = "{Dim} {value}"
tag = "Tag {key}={value}"

[dims]
organization = "Organisation {value}"

# One condition's heading, over its dimension's.
[values]
"stage=first" = "First rounds"
```

`{value}` is the condition's value, `{dim}` its dimension, `{Dim}` the same capitalized, and `{key}` a tag's key. A key of `[dims]` and the `<dim>` of a key of `[values]` name a dimension of the table above; an unknown key, or a heading of no dimension (`"round=first"`), fails the profile's load, naming the file and the key.

## Limits

A block is at most 16 KiB with its includes. A turn's judgment over 48 KiB drops blocks from the end of the order, never a root block, and the turn's sources list each one dropped.

## Commands

```sh
{{command}} profile where <intent> [conditions]
{{command}} profile add <intent>[,<intent>...] [conditions] [--section <h>] [--title <t>] [--override] [--paths <glob>]... [--apply] [-- <text>]
{{command}} profile check [<dir>]
{{command}} profile show <owner/repo|url> [--intent <intent>] [--show]
```

The conditions are one flag per dimension of the first twelve, `--organization`, `--repo`, `--user`, `--team`, `--association`, `--label`, `--base`, `--stage`, `--reviewer`, `--kind`, `--size` and `--risk`, and `--tag <key>=<value>` (repeatable). `profile add` writes a tag in its one spelling (`lang=Golang` as `lang=go`).
