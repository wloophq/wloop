---
title: Keys
description: The keys of the cockpit and the terminal client.
---

The same keys work in the web cockpit and the terminal client, on the selected entry or row. Lowercase keys and digits belong to the core; actions of the loop, of plugins and of intents use uppercase letters.

## Entries

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
| `r` | reply in the entry's conversation, saved to the Outbox (in the terminal, Tab sends it now) |
| `i` | edit a draft's text (terminal) |
| `e` | done |
| `Backspace` | dismiss |
| `o` | open the source |
| `n` | capture |
| `1`-`9` | answer that option, while a question card is focused; in the terminal, pick a draft the open detail lists when it asks no question |

## Navigation

| Key | Action |
|---|---|
| `g f`, `g i`, `g o`, `g n`, `g w`, `g p`, `g r` | Focus, Inbox, Outbox, Next, Waiting, Projects, Review |
| `g g` | the GitHub views |
| `Shift+1`..`9` | switch area |
| `/` | filter |
| `Cmd+K` (web), `Ctrl+K` (terminal) | the palette, with every action and its key |
| `?` | help |
| `Tab` | next view of the group (terminal) |
| `]` `[` | next and previous step of the Review |
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

In the terminal, actions that take text ask on the bottom line: Enter sends, Esc cancels. In the reply, edit and delegate prompts, Ctrl+E opens the text in `$VISUAL` or `$EDITOR`, and a draft of several lines opens there directly. Take over, run, nudge, and approving, retrying or discarding a draft ask for a `y` naming what they act on; a `y` typed in the same burst as the key before it does not count. `q` waits for running actions to finish (a second `q` leaves anyway).
