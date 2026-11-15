// A row's roving focus: the row, or a card's title, takes the Tab and its other controls are reached with the
// arrows, Home and End.

// rowStops are the stops of a row's roving focus in their order: the row itself when it takes the focus, then
// its controls that are shown and enabled.
export function rowStops(row: Element): Element[] {
  const out: Element[] = [];
  const walk = (n: Element) => {
    if ((n as HTMLElement).hidden || (n as HTMLElement).inert || n.getAttribute("disabled") != null) return;
    if (n.getAttribute("data-rove") != null) out.push(n);
    for (const c of n.children) walk(c);
  };
  walk(row);
  return out;
}

// ROVE_KEYS are how far each key moves a row's roving focus.
const ROVE_KEYS: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };

// roveKey moves the focus among a row's stops with the arrows, Home and End; true when it took the key.
export function roveKey(e: KeyboardEvent): boolean {
  const step = ROVE_KEYS[e.key];
  if (!step || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return false;
  const stops = rowStops(e.currentTarget as Element);
  const at = stops.indexOf(e.target as Element);
  if (at < 0) return false;
  e.preventDefault();
  const next = stops[Math.max(0, Math.min(stops.length - 1, at + step))];
  if (next !== e.target) (next as HTMLElement).focus();
  return true;
}

// markWrapped makes inert each child of box that wrapped below its first line, which the row never shows, so
// neither Tab nor the row's arrows reach it.
export function markWrapped(box: HTMLElement): void {
  const kids = [...box.children] as HTMLElement[];
  const top = kids.length ? kids[0].offsetTop : 0;
  for (const k of kids) k.inert = k.offsetTop > top;
}

// watchWrapped keeps box's wrapped children out of the focus order as they wrap and unwrap, and as they come and
// go; a card, whose pieces wrap in sight, passes on = false.
export function watchWrapped(box: HTMLElement, on = true) {
  if (!on) return {};
  markWrapped(box);
  const resized = typeof ResizeObserver === "function" ? new ResizeObserver(() => markWrapped(box)) : null;
  resized?.observe(box);
  const changed = typeof MutationObserver === "function" ? new MutationObserver(() => markWrapped(box)) : null;
  changed?.observe(box, { childList: true });
  return {
    destroy() {
      resized?.disconnect();
      changed?.disconnect();
    },
  };
}
