import { safeURL } from "./util";

// Agent prose as a tree of nodes that Markdown.svelte draws with elements and text only, so no text ever becomes
// markup: the subset agents write (paragraphs, emphasis, code, lists, links, headings, quotes), links only to
// https:// through safeURL. Only when the caller passes names, a mention @{<key>} reads as the name it gives its
// key, else the key; other prose keeps @{...} as written, since agents cite git refs like HEAD@{1}.

export type Inline =
  | string
  | { tag: "br" }
  | { tag: "code"; text: string }
  | { tag: "strong" | "em"; children: Inline[] }
  | { tag: "a"; href: string; children: Inline[] }
  | { tag: "mention"; key: string; text: string };

export type Block =
  | { kind: "code"; text: string }
  | { kind: "heading"; text: string }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "ul" | "ol"; items: string[] }
  | { kind: "p"; text: string };

export type Names = Record<string, string> | null | undefined;

const MD_FENCE = /^\s*(```|~~~)/;
const MD_HEADING = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/;
const MD_QUOTE = /^\s{0,3}>\s?/;
const MD_ITEM = /^\s{0,3}([-*+]|\d{1,9}[.)])\s+/;
const MD_BARE_URL = /https:\/\/[^\s<>()[\]]+/y;
const MD_WORD = /[\p{L}\p{N}]/u;
// MD_DEPTH caps nested quotes and emphases; deeper markers stay text in the innermost node.
export const MD_DEPTH = 8;

// markdownBlocks splits text into block descriptors; inline text is parsed only for the blocks drawn, so a folded
// text parses only what shows.
export function markdownBlocks(text: unknown): Block[] {
  return mdBlocks(String(text ?? "").replace(/\r\n?/g, "\n").split("\n"));
}

// foldedBlocks are the blocks a text shows folded to fold blocks; all of them when fold is 0 or the text is short.
export function foldedBlocks(blocks: Block[], fold: number): { shown: Block[]; folded: boolean } {
  const folded = fold > 0 && blocks.length > fold;
  return { shown: folded ? blocks.slice(0, fold) : blocks, folded };
}

function mdBlocks(lines: string[], depth = 0): Block[] {
  const out: Block[] = [];
  const quotes = depth < MD_DEPTH;
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = line.match(MD_FENCE);
    if (fence) {
      const body: string[] = [];
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
    if (quotes && MD_QUOTE.test(line)) {
      const body: string[] = [];
      while (i < lines.length && MD_QUOTE.test(lines[i])) body.push(lines[i++].replace(MD_QUOTE, ""));
      out.push({ kind: "quote", blocks: mdBlocks(body, depth + 1) });
      continue;
    }
    const item = line.match(MD_ITEM);
    if (item) {
      const ordered = /\d/.test(item[1]);
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].match(MD_ITEM);
        if (m && /\d/.test(m[1]) === ordered) items.push(lines[i++].slice(m[0].length));
        else if (items.length && lines[i].trim() && /^\s/.test(lines[i]) && !MD_ITEM.test(lines[i])) items[items.length - 1] += "\n" + lines[i++].trim();
        else break;
      }
      out.push({ kind: ordered ? "ol" : "ul", items });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !MD_FENCE.test(lines[i]) && !MD_HEADING.test(lines[i]) && !(quotes && MD_QUOTE.test(lines[i])) && !(para.length && MD_ITEM.test(lines[i]))) {
      para.push(lines[i++].trim());
    }
    out.push({ kind: "p", text: para.join("\n") });
  }
  return out;
}

// markdownInline turns one block's text into strings and inline nodes; a marker without its closing pair stays
// text. It runs in time linear in the text: closers and label ends are found once per text, not once per opener.
// Inside a link label it makes no link; past MD_DEPTH nested emphases the markers stay text.
export function markdownInline(s: string, names?: Names, depth = 0, links = true): Inline[] {
  const out: Inline[] = [];
  const closers = mdClosers(s);
  const labelEnd = links ? mdLabelEnds(s) : null;
  const keyEnd = names ? mdKeyEnds(s, true) : null;
  const groupEnd = names ? mdKeyEnds(s, false) : null;
  let start = 0;
  const flush = (i: number) => {
    if (i > start) out.push(s.slice(start, i));
  };
  const nested = depth < MD_DEPTH;
  // Every [ before the same ] shares its link target, scanned once.
  let targetOf = -1;
  let target: { url: string; end: number } | null = null;
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === "\n") {
      flush(i);
      out.push({ tag: "br" });
      start = ++i;
      continue;
    }
    if (c === "`") {
      const end = s.indexOf("`", i + 1);
      if (end > i + 1) {
        flush(i);
        out.push({ tag: "code", text: s.slice(i + 1, end) });
        start = i = end + 1;
        continue;
      }
    }
    if (nested && (c === "*" || c === "_")) {
      const double = s[i + 1] === c;
      const marker = double ? c + c : c;
      const open = !mdSpace(s, i + marker.length) && (double || !(c === "_" && mdWord(s, i - 1)));
      const end = open ? closers.next(marker, i + marker.length) : -1;
      if (end > 0) {
        flush(i);
        out.push({ tag: double ? "strong" : "em", children: markdownInline(s.slice(i + marker.length, end), names, depth + 1, links) });
        start = i = end + marker.length;
        continue;
      }
    }
    if (labelEnd && c === "[" && s[labelEnd[i + 1]] === "]" && labelEnd[i + 1] > i + 1) {
      const close = labelEnd[i + 1];
      if (close !== targetOf) {
        targetOf = close;
        target = mdLinkTarget(s, close + 1);
      }
      if (target) {
        flush(i);
        const label = markdownInline(s.slice(i + 1, close), names, depth + 1, false);
        const href = safeURL(target.url);
        if (href) out.push({ tag: "a", href, children: label });
        else out.push(...label);
        start = i = target.end;
        continue;
      }
    }
    const ends = names && (s.startsWith("group:", i + 2) ? groupEnd : keyEnd);
    if (names && ends && c === "@" && s[i + 1] === "{" && s[ends[i + 2]] === "}" && ends[i + 2] > i + 2) {
      const end = ends[i + 2];
      const key = s.slice(i + 2, end);
      flush(i);
      out.push({ tag: "mention", key, text: "@" + (Object.hasOwn(names, key) ? names[key] : key) });
      start = i = end + 1;
      continue;
    }
    if (links && c === "h" && !mdWord(s, i - 1) && s.startsWith("https://", i)) {
      MD_BARE_URL.lastIndex = i;
      const m = MD_BARE_URL.exec(s);
      const url = m ? m[0].replace(/[.,;:!?'"]+$/, "") : "";
      const href = safeURL(url);
      if (href) {
        flush(i);
        out.push({ tag: "a", href, children: [url] });
        start = i = i + url.length;
        continue;
      }
    }
    i++;
  }
  flush(i);
  return out;
}

function mdSpace(s: string, i: number): boolean {
  return i < 0 || i >= s.length || /\s/.test(s[i]);
}

function mdWord(s: string, i: number): boolean {
  return i >= 0 && i < s.length && MD_WORD.test(s[i]);
}

type Marker = "**" | "__" | "*" | "_";

// mdClosers lists, in one pass, every place an emphasis may close: the marker hugs text before it, a closing _
// does not sit inside a word, and a single marker is not half of a double one. next(marker, from) answers the
// first closer after from; openers ask in increasing order, so each list is walked once.
function mdClosers(s: string): { next(marker: string, from: number): number } {
  const at: Record<Marker, number[]> = { "**": [], __: [], "*": [], _: [] };
  for (let i = 1; i < s.length; i++) {
    const c = s[i];
    if ((c !== "*" && c !== "_") || mdSpace(s, i - 1)) continue;
    if (s[i + 1] === c && !(c === "_" && mdWord(s, i + 2))) at[(c + c) as Marker].push(i);
    if (s[i + 1] !== c && s[i - 1] !== c && !(c === "_" && mdWord(s, i + 1))) at[c].push(i);
  }
  const seen: Record<Marker, number> = { "**": 0, __: 0, "*": 0, _: 0 };
  return {
    next(marker, from) {
      const list = at[marker as Marker];
      let k = seen[marker as Marker];
      while (k < list.length && list[k] <= from) k++;
      seen[marker as Marker] = k;
      return k < list.length ? list[k] : -1;
    },
  };
}

// mdLinkTarget reads a link's (url) at open: no whitespace, parentheses only as balanced pairs one level deep.
// It answers the url and where the link ends, or null.
function mdLinkTarget(s: string, open: number): { url: string; end: number } | null {
  if (s.charAt(open) !== "(") return null;
  let j = open + 1;
  for (;;) {
    const c = s.charAt(j);
    if (c === "" || /\s/.test(c)) return null;
    if (c === ")") return j > open + 1 ? { url: s.slice(open + 1, j), end: j + 1 } : null;
    if (c === "(") {
      let k = j + 1;
      let d = s.charAt(k);
      while (d !== "" && d !== "(" && d !== ")" && !/\s/.test(d)) d = s.charAt(++k);
      if (d !== ")") return null;
      j = k + 1;
      continue;
    }
    j++;
  }
}

// mdLabelEnds holds, for each position, where a link label starting there would end: the next ] or line end.
function mdLabelEnds(s: string): Int32Array {
  const end = new Int32Array(s.length + 1);
  end[s.length] = s.length;
  for (let i = s.length - 1; i >= 0; i--) end[i] = s[i] === "]" || s[i] === "\n" ? i : end[i + 1];
  return end;
}

// mdKeyEnds holds, for each position, where a mention key starting there would end: the next brace or line end,
// or, when spaces end it as they end every key but a group's, the next brace or space.
function mdKeyEnds(s: string, spaces: boolean): Int32Array {
  const end = new Int32Array(s.length + 1);
  end[s.length] = s.length;
  const stop = spaces ? (ch: string) => /\s/.test(ch) : (ch: string) => ch === "\n";
  for (let i = s.length - 1; i >= 0; i--) end[i] = s[i] === "{" || s[i] === "}" || stop(s[i]) ? i : end[i + 1];
  return end;
}
