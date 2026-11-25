// Dispatches, at a point of the float's page, the pointer and mouse events of
// a pointer that entered, moved or exited, as the browser would: over and
// enter, move, out and leave. The element last under the pointer is kept in a
// global beside the bridge.
(function (bridge, kind, x, y) {
  var key = bridge + "Pointer";
  var s = window[key] || (window[key] = { el: null });
  var to = kind === "exited" ? null : document.elementFromPoint(x, y);
  var from = s.el;
  function chain(e) {
    var c = [];
    for (; e; e = e.parentElement) c.push(e);
    return c;
  }
  function fire(t, type, bubbles, rel) {
    var init = { bubbles: bubbles, cancelable: bubbles, clientX: x, clientY: y, relatedTarget: rel };
    t.dispatchEvent(new PointerEvent(type, Object.assign({ pointerType: "mouse", isPrimary: true }, init)));
    t.dispatchEvent(new MouseEvent(type.replace("pointer", "mouse"), init));
  }
  if (from !== to) {
    var fc = chain(from), tc = chain(to);
    if (from) {
      fire(from, "pointerout", true, to);
      fc.filter(function (e) { return tc.indexOf(e) < 0; }).forEach(function (e) { fire(e, "pointerleave", false, to); });
    }
    if (to) {
      fire(to, "pointerover", true, from);
      tc.filter(function (e) { return fc.indexOf(e) < 0; }).reverse().forEach(function (e) { fire(e, "pointerenter", false, from); });
    }
    s.el = to;
  }
  if (to) fire(to, "pointermove", true, null);
})
