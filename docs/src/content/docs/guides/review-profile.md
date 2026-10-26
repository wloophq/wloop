---
title: Write your review profile
description: Put your judgment in Markdown blocks, scoped by organization, repository, author or what the change is.
---

## Create the profile

`{{command}} init` can create a personal profile directory with a commented example block for each intent. To do it by hand, make a directory under `~/.config/{{name}}/profiles/` and name it in the configuration:

```json
"profile": "mine"
```

`profile` is a name under `~/.config/{{name}}/profiles/`, or a path. An empty profile adds nothing: the bundled guides still apply.

## Add a rule from a sentence

The `{{name}}-profile` skill places a rule for you. In any Claude Code session:

```text
every PR in acme must have a changelog entry
when reviewing dependabot PRs, only check the changelog
nudges should be shorter
```

The skill maps the sentence to intents and conditions, shows the diff and applies it on your yes. It drives the same commands you can run yourself:

```sh
{{command}} profile where review --organization acme        # which block a rule for acme reviews goes in
{{command}} profile add review,address --organization acme -- 'Every pull request adds an entry to CHANGELOG.md.'
{{command}} profile add review,address --organization acme --apply -- 'Every pull request adds an entry to CHANGELOG.md.'
{{command}} profile check
```

`profile add` prints the diff; `--apply` writes it, backs up each changed file outside a git work tree, and puts the old bytes back when the profile no longer loads. `--section <heading>` adds the text under that `##` heading.

## Write blocks by hand

A block is a Markdown file whose path holds its conditions and ends in `intent=<intent>.md`:

```text
~/.config/{{name}}/profiles/mine/
  intent=review.md                                 every review
  intent=address.md                                every address round
  intent=nudge.md                                  every nudge
  organization=acme/intent=review.md               reviews in acme's repositories
  organization=acme/repo=api/intent=review.md      reviews in acme/api
  user=dependabot[bot]/intent=review.md            reviews of dependabot's pull requests
  stage=follow-up/intent=review.md                 follow-up reviews only
  kind=fix/intent=review.md                        reviews of fixes (needs the brief)
  tags/lang=go/intent=review.md                    reviews of Go changes (needs the brief)
```

Each block's text goes into the turn under its own heading. Write it as instructions to the reviewer, in the shape of the bundled guide: short sections with short imperative bullets.

```markdown
## What to look for

- Every handler checks the tenant before it reads a row.
- A new endpoint has an entry in `docs/api.md`.

## Risky surfaces

Changes under `billing/` ask the user first.
```

A block may open with TOML front matter between `+++` lines:

```markdown
+++
title = "Acme's API rules"
paths = ["src/**/*.go"]
include = ["../policies/security.md"]

[delegates]
claude = ["/code-review high"]
+++
```

- `paths` applies the block only when a changed file matches one of the globs.
- `include` inlines up to eight `.md` or `.txt` files of the profile.
- `delegates` names, per agent, skills the finding step runs besides the procedure's own work.
- `override = true` drops the blocks above it on its path, the bundled one included, instead of adding to them.

The [profile format](/reference/profile-blocks/) has every condition, the order and the limits.

## Check what a round reads

```sh
{{command}} profile check                                  # loads the profile, lists its blocks
{{command}} profile show acme/api                          # every block per intent, and whether it applies there
{{command}} profile show https://github.com/acme/api/pull/7    # the same, with that pull request's facts
{{command}} profile show acme/api --show                   # the full instructions each intent would get
{{command}} profile try https://github.com/acme/api/pull/7     # the review it would publish, unpublished
```

A broken block (an unknown key, a bad value, a path off the grammar) stops every round, with the file and the fix, until you correct it. A change applies from the next round; a round already sent keeps the instructions it was sent with.
