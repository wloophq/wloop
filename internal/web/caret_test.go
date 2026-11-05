package web

import "testing"

// A sync rebuilds the detail while the person types in one of its text boxes, a reply's included: the rebuilt
// box takes the focus, the caret and the scroll back, whichever box it was.
func TestTheDetailKeepsTheCaretOfAnyBoxAcrossARender(t *testing.T) {
	var got map[string]any
	nodeRun(t, `
const CSS = { escape: (s) => s };
const document = { activeElement: null };
function box(slot) {
  return { dataset: { focus: slot }, selectionStart: 0, selectionEnd: 0, selectionDirection: "none", scrollTop: 0,
    focus() { document.activeElement = this; }, setSelectionRange(a, b, d) { this.selectionStart = a; this.selectionEnd = b; this.selectionDirection = d; } };
}
let kids = [];
const detail = { contains: (el) => kids.includes(el), querySelector: (sel) => kids.find((k) => sel === '[data-focus="' + k.dataset.focus + '"]') || null };
`, []string{"keepFocusIn"}, `
const reply = box("unit:reply:text");
kids = [box("detail:guidance"), reply];
reply.focus();
reply.selectionStart = 12; reply.selectionEnd = 12; reply.scrollTop = 40;
let rebuilt;
keepFocusIn(detail, () => { rebuilt = box("unit:reply:text"); kids = [box("detail:guidance"), rebuilt]; });
process.stdout.write(JSON.stringify({ focused: document.activeElement === rebuilt, start: rebuilt.selectionStart, end: rebuilt.selectionEnd, scroll: rebuilt.scrollTop }));`,
		nil, &got)
	if got["focused"] != true || got["start"] != float64(12) || got["end"] != float64(12) || got["scroll"] != float64(40) {
		t.Errorf("after a render the reply box holds %v, want the focus at 12 scrolled 40", got)
	}
}
