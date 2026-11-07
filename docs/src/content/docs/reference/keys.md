---
title: Keys
description: The keys of the cockpit and the terminal client.
---

The same keys work in the web cockpit and the terminal client, on the selected task or row. Lowercase keys and digits belong to the core; actions of the loop, of plugins and of intents use uppercase letters.

## Tasks

| Key | Action |
|---|---|
| `j` `k` | move the selection (the arrows too, in the terminal) |
| `Enter` / `Esc` | open / close the detail |
| `x` | to Next |
| `w` | to Waiting (asks on whom, optional) |
| `s` | schedule or snooze (asks the date, or `activity`) |
| `y` | to Someday |
| `l` | to Reference |
| `f` | toggle Focus |
| `p` | make a project or attach to one |
| `a` | approve the draft or proposal |
| `d` | delegate to an agent (asks for optional guidance; in the terminal, Tab hands it to a session) |
| `r` | reply in the task's conversation: sent now or held for your approval, as the reply verb's `send` in `tasks.toml` says (`now` in the bundled profile); in the terminal, Tab does the other |
| `i` | edit a draft's text (terminal) |
| `e` | done |
| `Backspace` | dismiss |
| `o` | open the source |
| `=` | group by the next field the view offers (terminal): the view's own, each other field, then none |
| `-` | sort by the next field the view offers (terminal): the view's own order, then each field ascending and descending |
| `n` | capture |
| `1`-`9` | the gate's intent of that number, in the order the gate lists them, while a gate is focused; in the terminal, pick a draft the open detail lists when it asks no question |
| `Cmd+Enter` | in a gate's answer box (web), send the answer with the option picked |

## Navigation

| Key | Action |
|---|---|
| `g i`, `g m`, `g w`, `g c`, `g n`, `g b`, `g t`, `g u`, `g a`, `g z`, `g f`, `g p`, `g d`, `g o`, `g r` | Inbox, Needs me, Waiting, Chase, Fits now, Blocked by me, Stalled, In loop, Approved, Snoozed, Focus, Projects, Done, Outbox, Review: each view's `key` in `views.toml`, and a view of yours adds its own (`g`, a space and a key no core key uses) |
| `g g` | the GitHub views: a plugin's own chord, which its UI declares (`key`), served while the plugin is enabled; a view's key in `views.toml` takes it first |
| `Shift+1`..`9` | switch area |
| `/` | filter |
| `Cmd+K` (web), `Ctrl+K` (terminal) | the palette, with every action and its key |
| `?` | help |
| `Tab` | next view of the group (terminal) |
| `]` `[` | next and previous step of the Review; while the focus is in a task's detail panel (web), the next and previous tab |
| `c` | copy a link to what shows, `{{command}}://open/#<route>` (web) |
| `t` | open the detail in a window of its own, while the focus is in the detail; a new tab in a browser (web) |
| `q`, `Ctrl+C` | quit (terminal) |

## Actions on a row

| Key | Action |
|---|---|
| `R` | Run now: send the pending or held round |
| `S` | Skip the pending round |
| `G` | Guide: send the round now with guidance |
| `T` | trust the folder and run |
| `D` | dismiss a block |
| `W` | answer the agent's question (a number picks that option, `y` or `n` answers a confirmation, other text is a free answer) |
| `C` | continue the stopped turn without an answer |
| `X` | dismiss the turn |
| `A` | take over |
| `H` | pause or resume |
| `M` | mute or unmute |
| `O` | open |
| `V` | review |
| `F` | address |
| `N` | nudge |

An intent of yours adds its own uppercase `key`.

In the cockpit, Tab moves from row to row, and on a board from card to card; a row's controls (its facet buttons, links and actions) are out of the Tab order, and Left and Right move between them and the row, Home and End to the first and the last. `j` and `k` move the selection from any of them. A draft's buttons in the Outbox keep their place in the Tab order.

In the terminal, actions that take text ask on the bottom line: Enter sends, Esc cancels. In the reply, edit and delegate prompts, Ctrl+E opens the text in `$VISUAL` or `$EDITOR`, and a draft of several lines opens there directly. Take over, run, nudge, and approving, retrying or discarding a draft ask for a `y` naming what they act on; a `y` typed in the same burst as the key before it does not count. `q` waits for running actions to finish (a second `q` leaves anyway).
