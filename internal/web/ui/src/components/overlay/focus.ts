// focusBack is the giver of the focus back to the element focused when an overlay opened, as a dialog does on
// close; an element gone from the page takes nothing.
export function focusBack(): () => void {
  const back = typeof document === "undefined" ? null : (document.activeElement as HTMLElement | null);
  return () => {
    if (back && back.isConnected && back.focus) back.focus();
  };
}
