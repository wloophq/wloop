// Conventional Comments (https://conventionalcomments.org): a comment that opens with "label (decorations):".

export interface Label {
  label: string;
  blocking: boolean;
}

export interface LabelCount extends Label {
  n: number;
}

const OPENING = /^\s*([a-z][a-z-]*)\s*(?:\(([^)]*)\))?\s*:(?!\/)/i;

// conventionalLabel is the label body opens with, blocking when a decoration says so; null when it opens with none.
export function conventionalLabel(body: string): Label | null {
  const m = OPENING.exec(String(body || ""));
  if (!m) return null;
  const decorations = (m[2] || "").split(",").map((d) => d.trim().toLowerCase());
  return { label: m[1].toLowerCase(), blocking: decorations.includes("blocking") };
}

// tally counts comments by label, the blocking groups first, then the larger, then the first seen.
export function tally(comments: { body: string }[]): LabelCount[] {
  const groups = new Map<string, LabelCount>();
  for (const c of comments) {
    const l = conventionalLabel(c.body);
    if (!l) continue;
    const key = l.label + (l.blocking ? "!" : "");
    const g = groups.get(key);
    if (g) g.n++;
    else groups.set(key, { ...l, n: 1 });
  }
  return [...groups.values()].sort((a, b) => Number(b.blocking) - Number(a.blocking) || b.n - a.n);
}
