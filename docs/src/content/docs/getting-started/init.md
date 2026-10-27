---
title: Set up with init
description: The guided setup that checks the requirements, writes the configuration and installs the agent hooks and the app.
---

`{{command}} init` takes you from a fresh install to a working loop. At a terminal it asks one question per step and suggests an answer for each. It is safe to run again: the current configuration supplies the defaults, so a second run is how you change an answer.

```sh
{{command}} init
```

## The steps

1. **Prerequisites.** It runs the checks of `{{command}} doctor` that need no daemon (`gh` and its login, `git`, the workspace managers, the agent). When one fails it stops and prints the fix.
2. **Projects directory.** `projects_dir` is where `{{command}} review` clones a repository that no workspace manager knows yet. It suggests the common parent of the repositories they already have.
3. **Agent.** The coding agent that runs the rounds, detected from what is installed. Claude Code (`claude`) is the one supported today.
4. **Scope.** What the daemon follows besides your own pull requests and your review requests: repositories (`github.scope.repos`), people (`github.scope.people`) and teams (`github.scope.teams`). It suggests them from what `gh` knows about you. See [the daemon](/concepts/daemon/#scope).
5. **Profile.** Either the bundled default profile alone, or a personal profile directory. A new directory is created with a commented example block for each intent, ready to edit. See [Write your review profile](/guides/review-profile/).
6. **Slack (optional).** Guided or skipped. The guided step walks you through creating the Slack app from the manifest, asks for the client id and the app-level token, and runs the OAuth login. See [Connect Slack](/guides/slack/).
7. **Configuration file.** It shows the diff against your current `config.toml` and asks before overwriting it.
8. **Install.** It offers `{{command}} install` (the agent homes) and, on macOS, `{{command}} app install` (the menu bar app and the LaunchAgents), each after a confirmation.
9. **Doctor.** It runs `{{command}} doctor` and prints the next step: usually [your first review](/getting-started/first-review/).

## Without questions

Every answer has a flag, so `init` runs in a script or a dotfiles setup:

```sh
{{command}} init \
  --projects-dir ~/projects \
  --agent claude \
  --scope-repo acme/api --scope-team acme/backend \
  --profile-default \
  --skip-slack \
  --yes
```

| Flag | Answers |
|---|---|
| `--projects-dir <dir>` | the projects directory |
| `--agent <name>` | the agent |
| `--scope-repo`, `--scope-team`, `--scope-person` | the scope, each repeatable |
| `--profile <dir>` or `--profile-default` | a personal profile directory (created with example blocks when absent), or the bundled profile alone |
| `--slack-client-id <id>` or `--skip-slack` | the Slack app's client id, or leave Slack as it is |
| `--no-install` | install into neither the agent homes nor the app |
| `--yes` | yes to every confirmation: overwrite the configuration, `install`, `app install` |

The flag set may grow; see `{{command}} init --help` for the current one.

## What it writes

- `~/.config/{{name}}/config.toml` (or under `$XDG_CONFIG_HOME`), only after you confirm the diff;
- the profile directory you chose, when it does not exist yet;
- what `{{command}} install` and `{{command}} app install` write, when you accept them (see [Install](/getting-started/install/)).

Every key it does not ask about keeps its default. The [configuration reference](/reference/configuration/) lists them all.
