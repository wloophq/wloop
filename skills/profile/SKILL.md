---
name: {{name}}-profile
description: Use when the user asks to add, change or remove a rule, guideline or convention in their {{name}} review profile, such as "every PR in acme must follow xyz", "when reviewing dependabot PRs, only check the changelog", or "nudges should be shorter". Places the rule in the right block of the profile through the {{name}} CLI.
---

# Putting a rule in the review profile

The profile is a tree of blocks, each a file whose path holds the conditions
under which it applies. `{{command}} profile` owns the paths and the checks:
never write or rename a block path by hand.

1. Map the request to intents. A convention the code must follow ("every PR
   must follow xyz") goes to `review` and `address`; how to judge other
   people's pull requests to `review`; how to answer reviews of the user's
   pull requests to `address`; the tone of reminders to `nudge`; an intent of the user's
   that names a `profile` takes blocks the same way. Map the
   scope to conditions: `--organization`, `--repo` (only with
   `--organization`), `--user` (the author), `--team`, `--association`,
   `--label`, `--base`, `--stage` (`first` or `follow-up`, review only),
   `--kind`, `--size`, `--risk`,
   `--tag <key>=<value>`. No condition means every pull request. Ask only
   when the scope is ambiguous.
2. Run `{{command}} profile where <intent> [conditions]` for each intent and
   read the file it names, if it exists, so the rule is not already there.
3. Write the rule as a short imperative paragraph, in the user's words. A
   rule about some files names them in its text ("in migrations, ...").
4. Run `{{command}} profile add <intent>[,<intent>...] [conditions] [--section <heading>] -- '<text>'`
   without `--apply`, with the text in single quotes (each `'` written as
   `'\''`), and show the user the diff it prints.
5. On the user's yes, run the same command with `--apply`, then
   `{{command}} profile check`.
6. To change or remove a rule, show the user the edit you plan to the file
   `{{command}} profile where` names, as a diff; on their yes, make it, then
   run `{{command}} profile check`.

`--title`, `--override` and `--paths` only start a new block, and apply to
every rule in it: `--paths <glob>` makes a block for those files alone. Pass
none of them to `profile where`. Kind, size, risk, tag and paths conditions
apply only to pull requests that have a brief; say so when the rule uses one.
A file a block includes must be a `.txt` inside the condition directories, or
sit in a directory with no condition. If a command fails, show its error and
fix the cause it names.
