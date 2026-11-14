import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { brand } from "../../brand";
import { I18N } from "../../lib/i18n.svelte";
import { page, update } from "../../state/build.svelte";
import { nav, readHash } from "../../state/route.svelte";
import { notice, state } from "../../state/store.svelte";
import { scheduler, setNotice } from "../../state/actions.svelte";
import { answer, fixtures, followHashes, settle, useCatalog, useRows } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import Notice from "./Notice.svelte";
import Pause from "./Pause.svelte";
import Search from "./Search.svelte";
import Sync from "./Sync.svelte";
import UpdateBanner from "./UpdateBanner.svelte";
import Warnings from "./Warnings.svelte";

const now = Date.parse("2026-11-07T12:00:00Z");
let written: string[];

beforeEach(() => {
  resetStore(now);
  written = followHashes();
  scheduler.load = () => {};
});

describe("the warnings", () => {
  test("say the newest trouble first, an action's failure with its detail folded", () => {
    const { container } = render(Warnings);
    const box = container.querySelector("#warnings") as HTMLElement;
    expect(box.hidden).toBe(true);
    state.health = { workspace_manager: "Orca", warnings: ["the profile has a typo"], jobs: [{ name: "ingest:github", last_error: "HTTP 502" }] };
    state.actionError = "review acme/api#7 failed: boom";
    state.actionDetail = "the workspace is gone";
    state.actionSource = "orca";
    state.conn = "closed";
    flushSync();
    expect(box.hidden).toBe(false);
    const lines = [...box.children].map((d) => d.firstChild!.textContent);
    expect(lines).toEqual([`disconnected: run \`${brand.command} open\``, "review acme/api#7 failed: boom", "the profile has a typo", "ingest:github: HTTP 502"]);
    expect(box.querySelector("summary")!.textContent).toBe("Orca's message");
    expect(box.querySelector("details")!.textContent).toBe("Orca's messagethe workspace is gone");
  });

  test("keep a detail the user opened open as other warnings come and go", () => {
    state.actionError = "review acme/api#7 failed: boom";
    state.actionDetail = "the workspace is gone";
    const { container } = render(Warnings);
    const details = container.querySelector("details") as HTMLDetailsElement;
    details.open = true;
    state.error = "Could not load: HTTP 500";
    state.health = { warnings: ["a warning"] };
    flushSync();
    expect(container.querySelector("details")).toBe(details);
    expect(details.open).toBe(true);
    expect(container.querySelector("#warnings")!.children.length).toBe(3);
  });
});

describe("the sync line", () => {
  test("names the connection until it opens, then the oldest sync, the budget and the version", () => {
    const { container } = render(Sync);
    const el = container.querySelector("#sync") as HTMLElement;
    expect(el.textContent).toBe("connecting");
    state.conn = "open";
    state.health = {
      paused: true, slow_sync: true, version: "v1.2.3", rate_limit: { limit: 5000, remaining: 1200 },
      jobs: [{ name: "ingest:github", last_ok: new Date(now - 5 * 60000).toISOString() }, { name: "ingest:slack", last_ok: new Date(now - 30000).toISOString() }, { name: "brief" }],
    };
    flushSync();
    expect(el.textContent).toBe("automatic rounds paused · synced 5 min ago · API budget 1,200 of 5,000 left · v1.2.3");
    expect(el.className).toBe("sync slow paused");
    state.health.jobs[0].running = true;
    state.health.paused = false;
    flushSync();
    expect(el.textContent).toBe("syncing · API budget 1,200 of 5,000 left · v1.2.3");
    expect(el.className).toBe("sync slow");
    state.conn = "reconnecting";
    flushSync();
    expect(el.textContent).toBe("reconnecting");
    expect(el.className).toBe("sync");
  });
});

describe("the pause button", () => {
  test("shows once the health is in, pauses or resumes, and takes no click while its request is on its way", async () => {
    let release!: () => void;
    const calls = answer(() => new Promise((r) => (release = () => r({ ok: true }))));
    const { container } = render(Pause);
    const b = container.querySelector("#pause") as HTMLButtonElement;
    expect(b.hidden).toBe(true);
    state.health = { paused: false };
    flushSync();
    expect(b.hidden).toBe(false);
    expect(b.textContent).toBe("Pause automatic rounds");
    await fireEvent.click(b);
    flushSync();
    expect(b.disabled).toBe(true);
    release();
    await settle();
    flushSync();
    expect(calls.map((c) => [c.url, c.body])).toEqual([["/api/pause", { paused: true }]]);
    expect(b.disabled).toBe(false);
    state.health = { paused: true };
    flushSync();
    expect(container.querySelector("#pause")).toBe(b);
    expect(b.textContent).toBe("Resume automatic rounds");
  });
});

describe("the notice", () => {
  test("says the last notice, an error in its class", () => {
    const { container } = render(Notice);
    const el = container.querySelector("#notice") as HTMLElement;
    setNotice("Copied a link to this view");
    flushSync();
    expect(el.textContent).toBe("Copied a link to this view");
    expect(el.className).toBe("notice");
    setNotice("Could not copy: denied", true);
    flushSync();
    expect(container.querySelector("#notice")).toBe(el);
    expect(el.className).toBe("notice err");
    expect(notice.err).toBe(true);
  });
});

describe("the update banner", () => {
  test("shows while the build check offers a reload, and Reload reloads", async () => {
    const reload = vi.fn();
    page.reload = reload;
    const { container } = render(UpdateBanner);
    const box = container.querySelector("#update") as HTMLElement;
    expect(box.hidden).toBe(true);
    update.shown = true;
    flushSync();
    expect(box.hidden).toBe(false);
    I18N.strings = { Reload: "Recarregar" };
    flushSync();
    expect(container.querySelector("#update-reload")!.textContent).toBe("Recarregar");
    await fireEvent.click(container.querySelector("#update-reload")!);
    expect(reload).toHaveBeenCalledOnce();
  });
});

describe("the filter", () => {
  function search(hash = "#view=mine") {
    nav.hash = hash;
    const { container } = render(Search);
    return container.querySelector("#search") as HTMLInputElement;
  }

  test("is the route's q, which typing writes in place", async () => {
    const box = search("#view=mine&q=risk%3Ahigh");
    expect(box.value).toBe("risk:high");
    expect(box.getAttribute("aria-keyshortcuts")).toBe("/");
    expect(box.type).toBe("search");
    box.value = "risk:high wait:me";
    await fireEvent.input(box, { inputType: "insertText", data: "e" });
    expect(readHash().q).toBe("risk:high wait:me");
    nav.hash = "#view=mine&q=ab";
    flushSync();
    expect(box.value).toBe("ab");
  });

  test("keeps its text, caret and focus while syncs change the list", async () => {
    useCatalog();
    useRows(fixtures.mineRows);
    const box = search();
    box.focus();
    box.value = "acme";
    box.setSelectionRange(2, 2);
    await fireEvent.input(box, { inputType: "insertText", data: "e" });
    useRows(fixtures.mineRows.slice(2));
    state.health = { viewer: "me" };
    flushSync();
    expect(document.activeElement).toBe(box);
    expect(box.value).toBe("acme");
    expect(box.selectionStart).toBe(2);
  });

  test("Escape clears it, then leaves it", async () => {
    const box = search("#view=mine&q=ab");
    box.focus();
    await fireEvent.keyDown(box, { key: "Escape" });
    expect(box.value).toBe("");
    expect(readHash().q).toBe("");
    expect(document.activeElement).toBe(box);
    await fireEvent.keyDown(box, { key: "Escape" });
    expect(document.activeElement).not.toBe(box);
  });

  test("Enter opens the only row it keeps", async () => {
    useCatalog();
    useRows(fixtures.mineRows);
    const box = search("#view=mine&q=t1");
    await fireEvent.keyDown(box, { key: "Enter" });
    expect(readHash().pr).toBe("github:acme/api#1");
    nav.hash = "#view=mine&q=acme";
    flushSync();
    written.length = 0;
    await fireEvent.keyDown(box, { key: "Enter" });
    expect(written).toEqual([]);
  });

  test("a pasted link of a row opens it; the same link typed filters", async () => {
    useCatalog();
    useRows([{ ...fixtures.mineRows[0], link: "https://github.com/acme/api/pull/1" }]);
    const box = search();
    box.value = "https://github.com/acme/api/pull/1";
    await fireEvent.input(box, { inputType: "insertText", data: "1" });
    expect(readHash().q).toBe("https://github.com/acme/api/pull/1");
    expect(readHash().pr).toBe("");
    box.value = "https://github.com/acme/api/pull/1/files";
    box.focus();
    await fireEvent.paste(box);
    await fireEvent.input(box, { inputType: "insertText", data: "x" });
    expect(box.value).toBe("");
    expect(readHash()).toMatchObject({ q: "", pr: "github:acme/api#1" });
    await new Promise((r) => setTimeout(r, 0));
    expect(document.activeElement).toBe(box);
  });

  test("rewords its label and placeholder when the strings change", () => {
    const box = search();
    I18N.strings = { "Filter the list": "Filtrar a lista" };
    flushSync();
    expect(box.getAttribute("aria-label")).toBe("Filtrar a lista");
  });
});
