---
title: Cockpit filter
description: The text and qualifiers the filter box takes, and how grouping and sorting work.
---

The filter box at the top of the cockpit (`/` focuses it) narrows every view to the pull requests whose number, repository, title, author, head branch, workspace, board column, reason, card comment, brief goal or brief tag holds every word typed, ignoring case and accents. Pasting a pull request URL opens it.

In the lists (Inbox, Next, Waiting, Someday, Projects, Outbox, Logbook) the same words narrow the tasks: their title, notes, state, kind, items, the people they wait on or come from, their channel or repository, project, area, contexts, and the text of their message or draft, ignoring case and the accents of Portuguese and Spanish. Qualifiers apply to the pull request views only. The terminal's `/` filter does the same.

The counts follow the filter in every view. The rail (the tabs in the terminal) counts what each view shows under the filter, the bar above a list reads "N of M" (what the filter shows of the view), and the group headings, the area picker and the context chips count the matches. With no free words, the counts of the lists are those of the whole views.

## Qualifiers

| Qualifier | Matches |
|---|---|
| `repo:` | the repository; `repo:api` matches any owner |
| `author:` | the author; `author:@me` for yours |
| `wait:` | whom it waits on, by role: `me`, `loop`, `reviewer`, `author`, `nobody` |
| `waits-on:` | a person or team it waits on |
| `kind:` | the situation |
| `type:` | the brief's kind: `feature`, `fix`, `refactor`, `chore`, `infra` |
| `risk:`, `size:` | the brief's risk and size |
| `status:` | the workspace's board column |
| `is:` | `stalled`, `needs-me`, `in-loop`, `approved` (the GitHub plugin's own reading of the row; the lens views of the same names list tasks, reading its link signals), `due` (an automatic round's plan gate deadline, mute end or brief retry within a day), `paused`, `muted`, `adopted`, `tracked` |
| `ci:` | `failure`, `pending`, `success`, `none`, or the name of a failing check |
| `opened:` | age since it opened: `1d`, `3d`, `7d`, `2w`, `4w`, `older` |
| `scope:` | a scope reason |
| `tag:` | a brief tag: `tag:lang:python` that tag, `tag:lang` any `lang:` tag |

Values of one qualifier widen, different qualifiers narrow, and a leading `-` leaves a value out:

```text
repo:api is:stalled -author:@me
wait:reviewer size:l size:xl
ci:failure tag:lang:go
```

Clicking a repository, author, person, situation, type, size, risk, tag or column on a row filters by it, Shift+click groups by it, and Alt+click leaves it out.

## Grouping, sorting and boards

The bar above the list groups (`g`), sorts (`s`, or a column header) and switches between list and board in any view; a board with no grouping groups by whom a row waits on. Grouping offers one "Tag `<key>`" per tag key present; a pull request with two values of that key shows in both groups. Filters, grouping and order stay in the URL and follow you across views.

## The Backlog view

Backlog charts the open pull requests above the list: what needs you, is stalled, sits in the loop, is approved or comes due; whom they wait on; repositories and situations; tag concentration; size against risk; how long each one has waited, with the stalled threshold and the 50th and 85th percentiles; what happens in the next 72 hours; and the tokens or US$ the loop spent on each. Clicking a bar, tile or point filters by it like a qualifier. "Include done" brings merged and closed pull requests back in.
