export const DETAIL_WIDTH_KEY = "loop.detailWidth";
export const DETAIL_WIDTH_MIN = 300;
export const DETAIL_WIDTH_STEP = 16;

// sheet is where the detail width is read and kept: the stylesheet's --detail-width and the browser's storage;
// tests replace it.
export const sheet = {
  cssWidth: (): string => getComputedStyle(document.documentElement).getPropertyValue("--detail-width"),
  storage: (): Storage => localStorage,
};

export function defaultDetailWidth(): number {
  const px = parseFloat(sheet.cssWidth());
  return Number.isFinite(px) ? px : DETAIL_WIDTH_MIN;
}

export function maxDetailWidth(viewport: number): number {
  return Math.max(DETAIL_WIDTH_MIN, Math.floor(viewport * 0.6));
}

export function clampDetailWidth(px: number, viewport: number): number {
  const w = Number.isFinite(px) ? Math.round(px) : defaultDetailWidth();
  return Math.min(maxDetailWidth(viewport), Math.max(DETAIL_WIDTH_MIN, w));
}

export function storedDetailWidth(viewport: number): number {
  try {
    const raw = sheet.storage().getItem(DETAIL_WIDTH_KEY);
    if (raw === null) return defaultDetailWidth();
    return clampDetailWidth(Number(raw), viewport);
  } catch {
    return defaultDetailWidth();
  }
}

export function saveDetailWidth(px: number | null): void {
  try {
    if (px === null) sheet.storage().removeItem(DETAIL_WIDTH_KEY);
    else sheet.storage().setItem(DETAIL_WIDTH_KEY, String(px));
  } catch {
    // The width still applies until the page reloads.
  }
}
