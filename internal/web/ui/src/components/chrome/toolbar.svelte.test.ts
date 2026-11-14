import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { I18N } from "../../lib/i18n.svelte";
import { facetValues, fieldById, lensOf, specFor, viewQueryKey, type Lens } from "../../lib/lens";
import type { J } from "../../lib/util";
import { lensResult } from "../../lib/view";
import { readHash } from "../../state/route.svelte";
import { nav } from "../../state/route.svelte";
import { tasksState } from "../../state/store.svelte";
import { clone, fixtures, followHashes, queryOf, useCatalog, useRows } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { served } from "../../state/keycaps.svelte";
import LensBar from "./LensBar.svelte";
import { filterFields, filterPalette, filterValues, lensItems } from "./toolbar";

let written: string[];

beforeEach(() => {
  resetStore(fixtures.lensNow);
  served.bindings = [];
  written = followHashes();
  useCatalog({ slack: true });
  tasksState.key = viewQueryKey("needs-me", "", []);
  tasksState.tasks = clone(fixtures.mixedTasks);
  useRows(fixtures.mineRows);
});

function lensAt(hash: string): Lens {
  nav.hash = hash;
  return lensOf(readHash());
}

function bar(hash: string) {
  nav.hash = hash;
  const props = $state({ lens: lensOf(readHash()), res: null as J });
  props.res = lensResult(props.lens);
  const r = render(LensBar, props);
  const focus = (slot: string) => r.container.querySelector(`[data-focus="${slot}"]`) as HTMLElement;
  return { ...r, props, focus };
}

describe("the lens bar", () => {
  test("shows each filter as a removable chip", async () => {
    const { container } = bar("#view=mine&q=wait%3Ame+risk%3Ahigh");
    const chips = [...container.querySelectorAll(".chip")] as HTMLElement[];
    expect(chips.map((c) => c.textContent)).toEqual(["Waits on (role): me ×", "Risk: high ×"]);
    expect(chips.map((c) => c.getAttribute("aria-label"))).toEqual(["Remove filter Waits on (role): me", "Remove filter Risk: high"]);
    await fireEvent.click(chips.find((c) => c.textContent!.startsWith("Risk"))!);
    expect(written.map(queryOf)).toEqual(["wait:me"]);
  });

  test("the Sort picker starts with the view's own order and offers what the old column heads sorted by", () => {
    const filled = (lens: Lens, items: J[], id: string) => items.some((it) => facetValues(it, id).length);
    const oldColumnSorts = (lens: Lens, items: J[]) => {
      const out = new Set<string>();
      for (const parts of [["strip", "who", "kind"], ["age"], ["aside"], ["ref"]]) {
        for (const part of parts) {
          if (part === "strip") {
            const f = lens.strip ? fieldById(lens.strip, lens.fields) : null;
            if (f && f.sort && filled(lens, items, f.id)) {
              out.add(f.id);
              break;
            }
            continue;
          }
          const s = (lens.specs || []).find((x: J) => {
            const f = fieldById(x.id, lens.fields);
            return f && f.sort && filled(lens, items, f.id) && specFor(x, lens.provider || "").show === part;
          });
          if (s) {
            out.add(s.id);
            break;
          }
        }
      }
      return [...out];
    };
    for (const view of ["mine", "needs-me"]) {
      const { container, unmount } = bar("#view=" + view);
      const lens = lensAt("#view=" + view);
      const old = oldColumnSorts(lens, lensItems(lens));
      const options = [...container.querySelectorAll('[data-focus="lens:sort"] option')].map((o) => (o as HTMLOptionElement).value);
      expect(old.length, view).toBeGreaterThan(0);
      expect(options[0], view).toBe("");
      for (const id of old) expect(options, view).toContain(id);
      unmount();
    }
  });

  test("hints the served key of filter:add, none before the keys load", () => {
    const { focus } = bar("#view=mine");
    const pill = focus("lens:add-filter");
    expect(pill.querySelector("kbd")).toBeNull();
    expect(pill.hasAttribute("aria-keyshortcuts")).toBe(false);
    served.bindings = [{ action: "filter:add", key: "=" }];
    flushSync();
    expect(focus("lens:add-filter")).toBe(pill);
    expect(pill.querySelector("kbd")!.textContent).toBe("=");
    expect(pill.getAttribute("aria-keyshortcuts")).toBe("=");
    served.bindings = [{ action: "filter:add", key: "+" }];
    flushSync();
    expect(pill.querySelector("kbd")!.textContent).toBe("+");
    expect(pill.getAttribute("aria-keyshortcuts")).toBe("Plus");
  });

  test("+ Filter lists the fields to filter by, then the picked field's values, and picking one filters", () => {
    const lens = lensAt("#view=mine");
    const items = lensItems(lens);
    const palettes: J[] = [];
    expect(filterPalette(lens, items, (p) => palettes.push(p))).toBe(true);
    const offered = filterFields(lens, items);
    expect(palettes[0].title).toBe("Filter");
    expect(offered.length).toBeGreaterThanOrEqual(2);
    expect(palettes[0].items.map((it: J) => it.label)).toEqual(offered.map((f) => f.label));
    const risk = offered.find((f) => f.id === "risk")!;
    palettes[0].items.find((it: J) => it.label === risk.label).run();
    expect(palettes[1].title).toBe("Risk");
    expect(palettes[1].items.map((it: J) => it.label)).toEqual(["high", "medium", "low", "no brief"]);
    palettes[1].items[0].run();
    expect(written.map(queryOf)).toEqual(["risk:" + filterValues(risk, items)[0].value]);
  });

  test("keeps its pickers in place while the list under it changes", () => {
    const { focus, props } = bar("#view=mine");
    const group = focus("lens:group");
    const count = () => focus("lens:copy-list").parentElement!.querySelector(".lens-count")!.textContent;
    group.focus();
    expect(count()).toBe("5 of 5");
    useRows(fixtures.mineRows.slice(1));
    props.lens = lensOf(readHash());
    props.res = lensResult(props.lens);
    flushSync();
    expect(focus("lens:group")).toBe(group);
    expect(document.activeElement).toBe(group);
    expect(count()).toBe("4 of 4");
  });

  test("rewords itself when the strings change, its controls in place", () => {
    const { focus } = bar("#view=mine");
    const copy = focus("lens:copy-list");
    I18N.locale = "pt-BR";
    I18N.strings = { "Copy list": "Copiar lista", Group: "Agrupar" };
    flushSync();
    expect(focus("lens:copy-list")).toBe(copy);
    expect(copy.textContent).toBe("Copiar lista");
    expect(focus("lens:group").previousElementSibling!.textContent).toBe("Agrupar");
  });

  test("Recent shows only its filters, the ones it cannot apply off; Explore and an unfiltered Recent none", () => {
    const recent = bar("#view=recent&q=where%3Aacme%2Fapi+risk%3Ahigh");
    const chips = [...recent.container.querySelectorAll(".chip")];
    expect(chips.map((c) => c.className)).toEqual(["chip pill on", "chip pill on off"]);
    expect(chips[1].getAttribute("title")).toBe("Not applied in Recent");
    expect(recent.focus("lens:clear")).not.toBeNull();
    recent.unmount();
    for (const hash of ["#view=recent", "#view=explore"]) {
      const { container, unmount } = bar(hash);
      const tools = container.querySelector("#tools") as HTMLElement;
      expect(tools.hidden, hash).toBe(true);
      expect(tools.children.length, hash).toBe(0);
      unmount();
    }
  });
});
