# Badge

Read-only labels on a meta line: `pill`, `pill tag`, risk, and `flag`.

- **pill**: `pill` text on `hover`, `muted`, rounded. A situation word ("agent asks", "review").
- **tag**: the same in `code` (mono) for `key:value` tags from the brief.
- **risk**: `risk-medium` is `late` on `late-bg`; `risk-high` is `err` on `err-bg`. Low risk is a plain pill. Always spelled out.
- **flag**: `flag` style (10px mono uppercase), outlined in `rule`, `radius-sm`. `mine` uses `mine-text`.

## Consumer provides
The text. Tags show at most three, then `+n`.
