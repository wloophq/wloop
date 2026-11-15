<script lang="ts">
  import type { Snippet } from "svelte";
  import { tick } from "svelte";
  import { catalog, collapsed, lists, state } from "../../state/store.svelte";
  import { minute } from "../../state/clock.svelte";
  import { readHash } from "../../state/route.svelte";
  import { listless, isCoreView, isPluginView, DASH, HOME, PLAN, QUEUE, RECENT } from "../../lib/catalog";
  import { fillWord, pipeline, type Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { readLens } from "../../lib/view";
  import Activity from "./Activity.svelte";
  import Board from "./Board.svelte";
  import EmptyNote from "./EmptyNote.svelte";
  import RowList from "./RowList.svelte";
  import { emptyNote, revealOpenRow, rowModel, unitTitle } from "./model";

  // The unit list of a row view: its count line, its rows or its board, the recent activity and what an empty
  // list says. tools is the lens bar, drawn under the count line.
  let { tools = undefined }: { tools?: Snippet<[Lens, J]> } = $props();

  const route = $derived(readHash());
  const lens = $derived(readLens());
  const view = $derived(lens.view);
  const none = $derived(listless(view));
  const res = $derived(none ? null : pipeline(state.rows, lens));
  const board = $derived(lens.layout === "board");
  const now = $derived(minute());
  const model = (kind: string) => (r: J, copy: string) => rowModel(r, route.pr, lens, copy, kind, now);
  const boardTitle = $derived((catalog.views.find((v) => v.id === view) || {}).label || view);

  let box: HTMLDetailsElement;

  $effect.pre(() => {
    const group = res ? res.group : lists.collapsedFor;
    if (lists.collapsedFor !== group) {
      collapsed.clear();
      lists.collapsedFor = group;
    }
  });

  $effect(() => {
    if (view !== DASH && box) box.open = true;
  });

  $effect(() => {
    const pr = route.pr;
    void res;
    tick().then(() => revealOpenRow(pr, box));
  });
</script>

<details
  bind:this={box}
  id="list-box"
  class={["list-box", view === DASH && "in-dash"]}
  hidden={view === PLAN || view === HOME || view === QUEUE || isPluginView(view) || isCoreView(view)}>
  <summary id="list-sum" class="list-sum">{res ? fillWord((lens.words || {}).list_count, { unit: unitTitle(view, lens), shown: res.shown, total: res.total }) : ""}</summary>
  {#if tools}{@render tools(lens, res)}{/if}
  <div id="table" hidden={none || board}>
    {#if res && !board}<RowList {res} model={model("row")} {lens} />{/if}
  </div>
  <Board id="board" groups={res && board && res.shown ? res.groups : []} {lens} title={boardTitle} model={model("card")} hidden={none || !board || !res || !res.shown} />
  <Activity {lens} hidden={view !== RECENT} />
  <p id="empty" class="empty" hidden={none || !res || res.shown > 0}>{#if res && !res.shown}<EmptyNote parts={emptyNote(view, lens.q)} />{/if}</p>
</details>
