wloop is a work loop for the reviews and pull requests that reach you, with agents doing the rounds. Its interface exists to answer one question at a glance, **what waits on me, and for how long**, and to let you act on it in one step. Everything below serves that question: color says *whom* something waits on, mono figures say *how long*, and the detail panel says *why* and *what to do*.

The interface runs on four surfaces that share one vocabulary: the web cockpit (`wloop open`), the terminal client (`wloop tui`), the macOS menu bar with its notifications, and the documentation site. The cockpit is the reference; the others borrow its words, its states and, where they have color, its hues.

## Principles

1. **Waits-on is the primary signal.** Every unit carries one waiter: `me`, `author`, `reviewer`, `loop` or `nobody`. Show it as a strip on the leading edge and as the `who` word. Never reuse those five hues for anything else.
2. **Time is the second signal.** Ages are mono (`age`), right where the eye lands after the strip. Past the stalled threshold an age turns `late` on `late-bg`; nothing else in a row uses that pair except medium risk.
3. **The daemon decides, the client renders.** Actions, their labels and their order come from the daemon. A surface never invents an action or reorders the recommended one away from first.
4. **Dense, calm, legible.** 13px body, hairline rules, no shadows, no gradients. Density comes from tight spacing on the 2/4/6/8 steps, never from smaller text than `flag`.
5. **Prose is sans, identifiers are mono.** Titles, agent questions and drafts read in `body`. Refs, ages, counts, timestamps, commands and event data are `code`.
6. **One channel per layer.** On a task row, shape is the task's state (the state glyph), words and glyphs are the ask (the item), and a neutral label is the source (the source chip). Color stays with waits-on and time.
7. **Open by construction.** Plugins, intents and workflows come and go: the profile overrides and adds intents and workflows, and new plugins bring new kinds of units. The interface defines frames and slots and fills them from the catalog; it never names a provider or an intent, and a plugin gets a mark, never a color.

## Content fundamentals

- **Voice.** Plain, short, declarative English. Say what happened and what will happen: "Automatic rounds paused", "A new version is running.", "Round in 60 s".
- **Casing.** Sentence case everywhere: views ("Needs me", "My PRs", "In loop"), buttons ("Run now", "Copy review request"), headings. Overlines are the only uppercase, set by the `overline` style, not typed.
- **Buttons are verbs**, one or two words: Review, Address, Nudge, Pause, Mute, Skip, Continue, Dismiss, Apply, Reload. A longer button names its object: "Copy list", "Send answer".
- **Tooltips explain consequences** in a full sentence: "The agent goes on with the turn without an answer", "Close the question; the pull request goes back to whoever it waits on".
- **Person.** "You" and "me" are the user ("Needs me", "waits for you", `author:@me`). The agent is "the agent"; the background service is "the loop" or "the daemon", never "we".
- **Numbers.** Ages as compact units, largest two: `10d 13h`, `2h 56m`, `3h 18m`. Counts bare and right-aligned in `muted` mono. "6 of 9" for a filtered total.
- **No emoji.** States use words and the waiter hues. The only glyphs are functional (see Iconography).
- **Empty states** say what would appear and why it is empty, in one `muted` line.

## Visual foundations

### Color

- **Surfaces.** `bg` is the page; `panel` raises the top bar, detail panel, cards, tiles and buttons. Hover with `hover`; select or press with `sel`. A row you own sits on `mine-bg`.
- **Text.** `ink` for primary, `muted` for everything secondary. Both hold 4.5:1 on every surface in both themes (a test in `internal/web/contrast_test.go` enforces it).
- **Waiters.** Strip and dot use the mark tokens (`me`, `author`, `reviewer`, `loop`, `none`); words use their `-text` twins. `none` is deliberately faint; the word carries it.
- **Accent.** `me` doubles as the app accent: primary button fill (text `on-me`), focus ring, current-view bar, selected row tint (`me` at 14% over `panel`). Because of that, keep at most one primary button per region.
- **Signals.** `late` on `late-bg` for time and medium risk; `err` on `err-bg` for failures and high risk; `loop-text` for good. Never signal by hue alone: each carries a word (late age, "risk high", "blocked").
- **Highlight.** `mark` behind `ink` for search matches only.

### Type

System faces only: `sans` for interface and prose, `mono` for figures and identifiers. No webfonts ship. Use the scale as defined: `heading` 16, `list-heading` 15, `body` and `row-title` 13, `meta` and `code` and `age` 12, `overline` and `pill` 11, `flag` 10. `kpi` 20 and `age-lg` 18 are the only large figures.

### Spacing and layout

- Steps: `space-2`, `space-4`, `space-6`, `space-8`, `space-10`, `space-12`, `space-16`, `space-24`. The page gutter is `space-16`.
- Three columns: rail (`rail-width`), list, detail (`detail-width`, resizable). Below `breakpoint-narrow` the rail wraps into a tab row with a bottom marker, the detail stacks under the list, and the Why and Where columns hide.
- Lists scroll; the top bar and table heads stick.

### Shape, borders, elevation

- Hairlines in `rule`, 1px; dashed for soft separators inside a list (timeline, activity) and for disabled or stale controls.
- Radii: `radius-md` for controls and cards, `radius-lg` for tiles and panels, `radius-pill` for chips, `radius-sm` for flags and the late age.
- No shadows. Elevation is `panel` over `bg` plus a `rule` border.
- The waits-on strip is the one colored edge: `strip-row` on rows and the situation block, `strip-card` on cards, tasks and the current rail view.

### States

- Hover: `hover` fill (buttons: border to `muted`).
- Pressed or on: `sel` fill and `me` border (`aria-pressed="true"`).
- Selected row or card: `me` 14% tint and an inset `me` bar.
- Focus: 2px solid `me` outline, 1px offset, on every interactive element.
- Disabled: 50% opacity with a progress cursor while busy; a dashed border and `muted` text when the action does not apply.
- Stale card: 75% opacity, dashed border.

### Motion

None beyond the browser's. Live updates replace content in place; the sync line reports freshness.

## Iconography

There is no icon set in the cockpit. Use text and a few Unicode glyphs:

- `▍` in `me` before the product name.
- `↑` `↓` after a sorted column head.
- `★` in `late` for a starred task.
- `×` for close and for removing a filter chip.
- `●` (the `dot` class) in the waiter hue beside a `who` word.
- State glyphs for tasks, drawn in CSS at `state-glyph` size: dashed circle (inbox), circle (next), half-filled circle (waiting), dotted circle (someday), circle with a dot (scheduled), square (reference), filled check (done), slashed circle (trash), `?` disc (message), `↗` square (draft). The terminal client draws them as `◌ ○ ◐ ◉ ◇ ▢ ● ⊘`.
- Item kinds: `→` request, `?` question, `◇` decision, `□` todo.
- Source marks: two uppercase letters of the provider in a `hover` tile, never a logo.

The menu bar notifications use four 128px PNG badges (assets group **Notifications**), each in the hue of what it means: `round` in `loop` (clock), `ask` in `me` (question), `held` in `late` (pause), `blocked` in `err` (stop). The docs site and the cockpit's favicon use the loop mark in `me` (**Logos**).

## Surfaces

| Surface | Uses |
|---|---|
| Cockpit | all tokens, both themes from `prefers-color-scheme` |
| Terminal client | the same views, keys and words; bold for the current view, dim for disabled actions; no hues of its own |
| Menu bar | `PR <n>` title, menu sections in cockpit order, the four notification badges |
| Docs | Starlight with `me` as its accent and the loop mark |

## Further sections

- **Information architecture**: the object model, the rail, the row grammar and the inspector across surfaces.
- **Tasks and sources**: the task layer after OmniFocus and Nirvana: state glyphs, the task row, items, source chips, lists, perspectives and the inspector.
- **Plugins and profiles**: the seam between a plugin's own model and the task layer, and the slots that place any intent's actions and any workflow's plan items.

Components marked *Proposed* in their guidelines describe the target design; the rest document the cockpit as it is.
