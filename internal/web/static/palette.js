"use strict";

const PALETTE_CSS = `
.pal-back { position: fixed; inset: 0; background: rgb(0 0 0 / 0.25); display: flex; justify-content: center; align-items: flex-start; padding-top: 12vh; z-index: 50; }
.pal { background: var(--panel); color: var(--ink); border: 1px solid var(--rule); border-radius: 8px; width: min(36rem, calc(100vw - 2rem)); max-height: 70vh; display: flex; flex-direction: column; box-shadow: 0 8px 32px rgb(0 0 0 / 0.25); font: 14px var(--sans); }
.pal-head { padding: 0.5rem 0.75rem; color: var(--muted); border-bottom: 1px solid var(--rule); }
.pal input { margin: 0.5rem 0.75rem; padding: 0.4rem 0.5rem; font: inherit; color: inherit; background: var(--bg); border: 1px solid var(--rule); border-radius: 4px; }
.pal ol, .pal dl { list-style: none; margin: 0; padding: 0 0 0.5rem; overflow: auto; }
.pal li { display: flex; justify-content: space-between; gap: 1rem; padding: 0.3rem 0.75rem; cursor: pointer; }
.pal li[aria-selected="true"] { background: var(--sel); }
.pal kbd { font: 12px var(--mono); color: var(--muted); }
.pal dt { padding: 0.5rem 0.75rem 0.2rem; color: var(--muted); font-weight: 600; }
.pal dd { margin: 0; display: flex; justify-content: space-between; gap: 1rem; padding: 0.15rem 0.75rem; }
`;

// paletteMatch keeps the items whose label or key holds every word of q.
function paletteMatch(items, q) {
  const words = String(q || "").toLowerCase().split(/\s+/).filter(Boolean);
  return items.filter((it) => {
    const text = `${it.label} ${it.key || ""}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}

function palEl(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat(Infinity)) if (c !== null && c !== undefined && c !== false) node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  return node;
}

// palOverlay shows box over the page; the returned close removes it and
// gives the focus back.
function palOverlay(box) {
  if (!document.getElementById("pal-css")) document.head.append(palEl("style", { id: "pal-css" }, PALETTE_CSS));
  for (const old of document.querySelectorAll(".pal-back")) old.remove();
  const back = document.activeElement;
  const shade = palEl("div", { class: "pal-back" }, box);
  const close = () => {
    shade.remove();
    if (back && back.isConnected && back.focus) back.focus();
  };
  shade.addEventListener("mousedown", (e) => { if (e.target === shade) close(); });
  document.body.append(shade);
  return close;
}

// showPalette lists items with their keys; typing narrows them, Enter runs
// the highlighted one, Esc closes.
function showPalette({ title, items }) {
  const input = palEl("input", { type: "text", "aria-label": title, placeholder: `${title}: type to narrow`, autocomplete: "off", spellcheck: "false" });
  const list = palEl("ol", { role: "listbox", "aria-label": title });
  const box = palEl("div", { class: "pal", role: "dialog", "aria-label": title }, palEl("div", { class: "pal-head" }, title), input, list);
  const close = palOverlay(box);
  let shown = items;
  let at = 0;
  const pick = (it) => {
    close();
    if (it) it.run();
  };
  const draw = () => {
    list.replaceChildren(...shown.map((it, i) => palEl("li", { role: "option", "aria-selected": String(i === at), onmousedown: (e) => { e.preventDefault(); pick(it); } },
      palEl("span", {}, it.label), it.key ? palEl("kbd", {}, it.key) : null)));
  };
  input.addEventListener("input", () => {
    shown = paletteMatch(items, input.value);
    at = 0;
    draw();
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") close();
    else if (e.key === "Enter") pick(shown[at]);
    else if (e.key === "ArrowDown") at = Math.min(shown.length - 1, at + 1);
    else if (e.key === "ArrowUp") at = Math.max(0, at - 1);
    else return;
    e.preventDefault();
    draw();
  });
  draw();
  input.focus();
}

// showHelp lists every binding by context.
function showHelp(groups) {
  const dl = palEl("dl", {}, groups.map((g) => [palEl("dt", {}, g.head), g.bindings.map((b) => palEl("dd", {}, palEl("span", {}, b.help || b.action), palEl("kbd", {}, b.key)))]));
  const box = palEl("div", { class: "pal", role: "dialog", "aria-label": "Keys", tabindex: "-1" }, palEl("div", { class: "pal-head" }, "Keys (Esc closes)"), dl);
  const close = palOverlay(box);
  box.addEventListener("keydown", (e) => {
    if (e.key === "Escape" || e.key === "?") {
      e.preventDefault();
      close();
    }
  });
  box.focus();
}

// askInput is the small input a key opens: the typed text on Enter, null on Esc.
function askInput({ label, placeholder }) {
  return new Promise((resolve) => {
    const input = palEl("input", { type: "text", "aria-label": label, placeholder: placeholder || "", autocomplete: "off" });
    const box = palEl("div", { class: "pal", role: "dialog", "aria-label": label }, palEl("div", { class: "pal-head" }, label), input);
    let done = false;
    const close = palOverlay(box);
    const finish = (v) => {
      if (done) return;
      done = true;
      close();
      resolve(v);
    };
    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") finish(null);
      else if (e.key === "Enter" && !e.isComposing) finish(input.value);
      else return;
      e.preventDefault();
    });
    input.addEventListener("blur", () => finish(null));
    input.focus();
  });
}
