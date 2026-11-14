import { tick } from "svelte";
import { afterSync } from "../../state/sync.svelte";

// The keyboard works from the focused control. When that control goes away (a detail closed, an action no longer
// served, a facet whose value changed) the focus would fall to the page and the keys would start over from the
// top; it goes instead to the control's own slot drawn again, else to the row that held it.
const last = { slot: "", row: "" };

function remember(e: FocusEvent): void {
  const el = e.target as HTMLElement | null;
  if (!el || !el.closest) return;
  last.slot = el.getAttribute("data-focus") || "";
  last.row = (el.closest("[data-row]") as HTMLElement | null)?.getAttribute("data-row") || "";
}

function find(doc: Document, slot: string): HTMLElement | null {
  return slot ? doc.querySelector<HTMLElement>(`[data-focus="${CSS.escape(slot)}"]`) : null;
}

// rescueFocus puts the focus back when it fell to the page: on slot when it is given and drawn, else on the last
// focused control's slot, else on its row.
export async function rescueFocus(doc: Document = document, slot = ""): Promise<void> {
  await tick();
  const active = doc.activeElement;
  if (active && active !== doc.body && active.isConnected) return;
  for (const s of [slot, last.slot, last.row]) {
    const el = find(doc, s);
    if (el && !(el as HTMLButtonElement).disabled) {
      el.focus({ preventScroll: true });
      return;
    }
  }
}

// followFocus remembers the focused control of doc and rescues the focus after each read the page applies; the
// returned stop removes both.
export function followFocus(doc: Document = document): () => void {
  const rescue = () => void rescueFocus(doc);
  doc.addEventListener("focusin", remember);
  afterSync.add(rescue);
  return () => {
    doc.removeEventListener("focusin", remember);
    afterSync.delete(rescue);
  };
}
