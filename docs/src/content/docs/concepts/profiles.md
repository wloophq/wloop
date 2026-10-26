---
title: Profiles and intents
description: How your judgment reaches every round, and how a profile adds kinds of turns.
---

A turn's instructions have two parts:

- The **procedure** is the mechanics of the turn: reading the pull request, delegating, the ambiguity gate, the rule that an automatic round never approves on its own, publishing, the record that ends it. It comes from the intent's instructions, rendered for each turn with only the steps that turn runs. The bundled intents are defaults: your profile overrides any of their files, as [Change a bundled intent](/guides/custom-intent/#change-a-bundled-intent) shows. The shared partials, such as the record protocol, are files of the profile too: a `partials/<name>.md.tmpl` of yours replaces the bundled one for every intent, as [Change a partial](/guides/custom-intent/#change-a-partial) shows.
- The **judgment** is yours: what a review looks for, the labels and what blocks, the risky surfaces, the shape of the body, the voice; how you answer reviews of your pull requests, commit and test the fixes, and whom you ask for another look; what a nudge says and its tone.

## The review profile

Your **review profile** is one directory of **blocks**: Markdown files whose path says when each applies. The configuration's `profile` names it, either a name under `~/.config/{{name}}/profiles/` or a path. {{name}}'s bundled guides are the layer below it: they come first, and your blocks add to them.

```text
intent=review.md                               every review
organization=acme/intent=review.md             reviews in acme's repositories
organization=acme/repo=api/intent=address.md   address rounds in acme/api
user=dependabot[bot]/intent=review.md          reviews of that author's pull requests
tags/change=refac/intent=review.md             reviews whose brief tags the change a refactor
```

Every block that matches the pull request goes into the turn, in a fixed order, each under its own heading: `{{name}}'s review guide`, then `Your review guide`, then `Organization acme`, and so on. Where two blocks disagree, the later one wins; a block with `override = true` drops the blocks above it on its path. The [profile format](/reference/profile-blocks/) has the full grammar.

The bundled guides, `intent=review.md`, `intent=address.md` and `intent=nudge.md`, hold every default you may want to change: what to look for, risky surfaces, Conventional Comments labels, the body and the voice of a review; the stance, commits, replies and whom to ask again in an address round; the content, tone and form of a nudge. A nudge always mentions exactly the people the pull request waits on, says what it waits for and since when, asks for one next step and posts one comment; your `intent=nudge.md` blocks set tone, length, language and sign-off.

```sh
{{command}} profile check                       # load the profile, list its intents and blocks
{{command}} profile show acme/api               # which blocks a round in acme/api reads
{{command}} profile try <url>                   # the review your profile would publish, unpublished
```

## Intents

An **intent** is a kind of turn: `review`, `address`, `nudge`, `act`, the `probe`, or one your profile adds. Each is a manifest, `intent.toml`, plus the turn's instructions template, `instructions.md.tmpl`. From the manifest {{name}} builds:

- a command: `{{command}} <intent> <url> [flags] [guidance...]`, listed by `{{command}} --help`; an intent that serves another provider takes that provider's unit, such as a Slack permalink or `slack:<key>`;
- a way to run it by hand from any agent session: `{{command}} instructions <intent>`, or the bundled skills such as `/{{name}}-review <url>`;
- an action on the rows of the cockpit, the terminal client and the menu bar, with its own key.

A turn of any intent records, asks, takes answers and resumes the same way. A user intent runs only when you ask; what starts a turn on its own is a [workflow](/concepts/workflows/) whose `turn.start` step names the intent. A directory of your profile named as a bundled intent or workflow overrides it file by file.

| Bundled intent | Key | Runs in | Does |
|---|---|---|---|
| `review` | `V` | the review workspace, at the pull request's head | reviews someone else's pull request and publishes the review |
| `address` | `F` | the author workspace | triages the reviews and failing checks of a pull request you develop |
| `nudge` | `N` | the pull request's workspace, else the repository's main checkout | asks whoever the pull request waits on for the next step, in one comment |
| `act` | | headless, or a session | resolves the open items of a unit and proposes the replies as drafts |
| `probe` | | a workspace you name | a self-test of an agent home (`{{command}} probe`) |

See [Add your own intent](/guides/custom-intent/) and the [intent manifest](/reference/intent-manifest/).

## The skills

`{{command}} install` installs five skills into every agent home:

| Skill | Use |
|---|---|
| `{{name}}-review` | `/{{name}}-review <url> [guidance]` runs a review round from the session you are in |
| `{{name}}-address` | `/{{name}}-address <url>` runs an address round from the session you are in |
| `{{name}}-nudge` | `/{{name}}-nudge <url>` runs a nudge |
| `{{name}}-profile` | adds a rule to your profile from a sentence such as "every PR in acme must follow xyz" |
| `{{name}}` | lets an agent session drive the CLI: start reviews, read the status, explain what the loop is doing |

In Codex the same skills run as `$<skill> <url>`. `{{command}} install --print-skill <name>` prints one.

The review, address and nudge skills come from one template, filled from each intent's manifest; an intent gets one when its manifest sets `skill = true`. Every installed skill carries a marker in its front matter:

```yaml
metadata:
  managed-by: {{name}}
  version: "0.1.0"
  sha256: 3f1c...
```

`sha256` is the digest of the file without that line. `install` replaces a skill whose marker is {{name}}'s and whose digest still matches, whatever version wrote it; a skill you edited, or one without the marker, stays as it is, and `install` and `doctor` say so. `uninstall` removes the same unedited skills and leaves the others.
