// The overlays the page asks through: a confirm, a one-line input, a palette of items and the keys' help. Each
// is a request in this stack until the overlay component that draws it finishes it, which settles its promise;
// the newest one is on top.

export interface PaletteItem {
  label: string;
  meta?: string;
  key?: string;
  run: () => unknown;
}

export interface PaletteRequest {
  title: string;
  items: PaletteItem[];
  source?: (q: string) => PaletteItem[] | Promise<PaletteItem[]>;
  empty?: string;
}

export interface HelpGroup {
  head: string;
  bindings: { key: string; action: string; help?: string }[];
}

export type Overlay =
  | { id: number; kind: "confirm"; text: string; finish: (v: boolean) => void }
  | { id: number; kind: "input"; label: string; placeholder: string; value: string; finish: (v: string | null) => void }
  | { id: number; kind: "palette"; palette: PaletteRequest; finish: () => void }
  | { id: number; kind: "help"; groups: HelpGroup[]; finish: () => void };

export const overlays = $state({ stack: [] as Overlay[] });

let seq = 0;

function push<T>(make: (id: number, settle: (v: T) => void) => Overlay): Promise<T> {
  return new Promise<T>((resolve) => {
    const id = ++seq;
    let done = false;
    const settle = (v: T) => {
      if (done) return;
      done = true;
      overlays.stack = overlays.stack.filter((o) => o.id !== id);
      resolve(v);
    };
    overlays.stack = [...overlays.stack, make(id, settle)];
  });
}

// askConfirm asks text in the page, as confirm would: true on Confirm, false on Cancel, Esc or a click outside.
// The native window's WebKit shows no browser dialog.
export function askConfirm(text: string): Promise<boolean> {
  return push<boolean>((id, finish) => ({ id, kind: "confirm", text, finish }));
}

// askInput is the small input a key opens: the typed text on Enter, null on Esc.
export function askInput({ label, placeholder = "", value = "" }: { label: string; placeholder?: string; value?: string }): Promise<string | null> {
  return push<string | null>((id, finish) => ({ id, kind: "input", label, placeholder, value: value || "", finish }));
}

// showPalette lists items in place of the overlays shown; typing narrows them, or asks source for the items of
// the text typed.
export function showPalette(palette: PaletteRequest): Promise<void> {
  dropOverlays();
  return push<void>((id, finish) => ({ id, kind: "palette", palette, finish: () => finish() }));
}

// showHelp lists every binding by context, in place of the overlays shown.
export function showHelp(groups: HelpGroup[]): Promise<void> {
  dropOverlays();
  return push<void>((id, finish) => ({ id, kind: "help", groups, finish: () => finish() }));
}

// omni is the omnibox open over the page, under the questions asked from it; null when it is closed. Each open is
// a new id, so opening it again starts it empty. Its text is what a reload would lose while it is open.
export const omni = $state({ open: null as null | { id: number; items: PaletteItem[]; text: string } });

let omniSeq = 0;

// dropOverlays takes every overlay off the page, the omnibox too; a question among them answers as a cancel.
export function dropOverlays(): void {
  closeOverlays();
  omni.open = null;
}

// showOmnibox opens the omnibox over the page in place of the overlays shown; items are the selection's actions
// it lists while nothing is typed.
export function showOmnibox(opts: { items?: PaletteItem[]; text?: string } = {}): void {
  dropOverlays();
  omni.open = { id: ++omniSeq, items: opts.items || [], text: opts.text || "" };
}

// closeOmnibox closes the omnibox and the questions asked from it.
export function closeOmnibox(): void {
  dropOverlays();
}

// closeOverlays dismisses every open overlay, as a confirm's Cancel would.
export function closeOverlays(): void {
  for (const o of [...overlays.stack]) {
    if (o.kind === "confirm") o.finish(false);
    else if (o.kind === "input") o.finish(null);
    else o.finish();
  }
}
