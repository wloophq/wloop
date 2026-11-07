"use strict";

// renderMarkdown draws agent prose as DOM nodes through h() and text nodes only, so no text ever becomes
// markup: the subset agents write (paragraphs, emphasis, code, lists, links, headings, quotes), links only
// to https:// through safeURL.

// MD_OPEN holds the keys of folded texts the reader opened, so a re-render keeps them open.
const MD_OPEN = new Set();

const MD_FENCE = /^\s*(```|~~~)/;
const MD_HEADING = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/;
const MD_QUOTE = /^\s{0,3}>\s?/;
const MD_ITEM = /^\s{0,3}([-*+]|\d{1,9}[.)])\s+/;
const MD_LINK = /^\[([^\]\n]+)\]\(((?:[^()\s]|\([^()\s]*\))+)\)/;
const MD_BARE_URL = /^https:\/\/[^\s<>()[\]]+/;
const MD_WORD = /[\p{L}\p{N}]/u;

function renderMarkdown(text, opts) {
  const o = opts || {};
  const blocks = mdBlocks(String(text ?? "").replace(/\r\n?/g, "\n").split("\n"));
  const folded = o.fold > 0 && blocks.length > o.fold && !(o.key && MD_OPEN.has(o.key));
  const shown = folded ? blocks.slice(0, o.fold) : blocks;
  const open = (ev) => {
    if (o.key) MD_OPEN.add(o.key);
    const box = ev && ev.currentTarget && ev.currentTarget.closest ? ev.currentTarget.closest(".md") : null;
    if (box) box.replaceWith(renderMarkdown(text, { ...o, fold: 0 }));
  };
  return h("div", { class: "md" }, shown.map(mdBlock), folded ? h("button", { type: "button", class: "md-more", onclick: open }, t("Read all")) : null);
}

// mdBlocks splits lines into block descriptors without building nodes, so a folded text builds only what shows.
function mdBlocks(lines) {
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = line.match(MD_FENCE);
    if (fence) {
      const body = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++;
      out.push({ kind: "code", text: body.join("\n") });
      continue;
    }
    const heading = line.match(MD_HEADING);
    if (heading) {
      out.push({ kind: "heading", text: heading[1] });
      i++;
      continue;
    }
    if (MD_QUOTE.test(line)) {
      const body = [];
      while (i < lines.length && MD_QUOTE.test(lines[i])) body.push(lines[i++].replace(MD_QUOTE, ""));
      out.push({ kind: "quote", blocks: mdBlocks(body) });
      continue;
    }
    const item = line.match(MD_ITEM);
    if (item) {
      const ordered = /\d/.test(item[1]);
      const items = [];
      while (i < lines.length) {
        const m = lines[i].match(MD_ITEM);
        if (m && /\d/.test(m[1]) === ordered) items.push(lines[i++].slice(m[0].length));
        else if (items.length && lines[i].trim() && /^\s/.test(lines[i]) && !MD_ITEM.test(lines[i])) items[items.length - 1] += "\n" + lines[i++].trim();
        else break;
      }
      out.push({ kind: ordered ? "ol" : "ul", items });
      continue;
    }
    const para = [];
    while (i < lines.length && lines[i].trim() && !MD_FENCE.test(lines[i]) && !MD_HEADING.test(lines[i]) && !MD_QUOTE.test(lines[i]) && !(para.length && MD_ITEM.test(lines[i]))) {
      para.push(lines[i++].trim());
    }
    out.push({ kind: "p", text: para.join("\n") });
  }
  return out;
}

function mdBlock(b) {
  switch (b.kind) {
    case "code":
      return h("pre", {}, h("code", {}, b.text));
    case "heading":
      return h("h4", {}, mdInline(b.text));
    case "quote":
      return h("blockquote", {}, b.blocks.map(mdBlock));
    case "ul":
    case "ol":
      return h(b.kind, {}, b.items.map((it) => h("li", {}, mdInline(it))));
    default:
      return h("p", {}, mdInline(b.text));
  }
}

// mdInline turns one block's text into strings and inline nodes; a marker without its closing pair stays text.
function mdInline(s) {
  const out = [];
  let plain = "";
  const flush = () => {
    if (plain) out.push(plain);
    plain = "";
  };
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    const prev = i > 0 ? s[i - 1] : "";
    if (c === "\n") {
      flush();
      out.push(h("br", {}));
      i++;
      continue;
    }
    if (c === "`") {
      const end = s.indexOf("`", i + 1);
      if (end > i + 1) {
        flush();
        out.push(h("code", {}, s.slice(i + 1, end)));
        i = end + 1;
        continue;
      }
    }
    if ((c === "*" || c === "_") && s[i + 1] === c) {
      const end = mdClose(s, c + c, i + 2);
      if (end > 0) {
        flush();
        out.push(h("strong", {}, mdInline(s.slice(i + 2, end))));
        i = end + 2;
        continue;
      }
    }
    if ((c === "*" || c === "_") && s[i + 1] !== c && !(c === "_" && MD_WORD.test(prev))) {
      const end = mdClose(s, c, i + 1);
      if (end > 0) {
        flush();
        out.push(h("em", {}, mdInline(s.slice(i + 1, end))));
        i = end + 1;
        continue;
      }
    }
    if (c === "[") {
      const m = s.slice(i).match(MD_LINK);
      if (m) {
        flush();
        const href = safeURL(m[2]);
        out.push(href ? h("a", { href, target: "_blank", rel: "noopener noreferrer" }, mdInline(m[1])) : mdInline(m[1]));
        i += m[0].length;
        continue;
      }
    }
    if (c === "h" && !MD_WORD.test(prev)) {
      const m = s.slice(i).match(MD_BARE_URL);
      if (m) {
        const url = m[0].replace(/[.,;:!?'"]+$/, "");
        const href = safeURL(url);
        if (href) {
          flush();
          out.push(h("a", { href, target: "_blank", rel: "noopener noreferrer" }, url));
          i += url.length;
          continue;
        }
      }
    }
    plain += c;
    i++;
  }
  flush();
  return out;
}

// mdClose finds the closing marker of an emphasis that opened right before from: it must hug text on both
// sides, and a closing _ must not sit inside a word.
function mdClose(s, marker, from) {
  if (from >= s.length || /\s/.test(s[from])) return -1;
  for (let at = s.indexOf(marker, from + 1); at > from; at = s.indexOf(marker, at + 1)) {
    const hugs = !/\s/.test(s[at - 1]);
    const free = marker[0] !== "_" || !MD_WORD.test(s[at + marker.length] || "");
    const whole = marker.length === 2 || (s[at + 1] !== marker && s[at - 1] !== marker);
    if (hugs && free && whole) return at;
  }
  return -1;
}
