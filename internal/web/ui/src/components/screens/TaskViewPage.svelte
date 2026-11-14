<script lang="ts">
  import type { Snippet } from "svelte";
  import { isCoreView, REVIEW, viewLabel, viewSpec } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { lensOf, pipelineOf, viewQueryKey, type Lens, type Pipeline } from "../../lib/lens";
  import { taskById, taskLensItems } from "../../lib/tasks";
  import type { J } from "../../lib/util";
  import { minute } from "../../state/clock.svelte";
  import { OUTBOX_LAYOUT } from "../../state/loaders.svelte";
  import { readHash, writeHash } from "../../state/route.svelte";
  import { detailState, outboxState, removeState, reviewState, tasksState } from "../../state/store.svelte";
  import GateCard from "../gates/GateCard.svelte";
  import Board from "../rows/Board.svelte";
  import RowList from "../rows/RowList.svelte";
  import { taskModel } from "../rows/model";
  import Outbox from "./Outbox.svelte";
  import Review from "./Review.svelte";
  import ViewIntro from "./ViewIntro.svelte";
  import { reviewTitle } from "./review";
  import { areaOptions, contextChips, removeAllClean } from "./views";

  // A core view's page: its head, the lens bar over its tasks (tools, given the lens and what it shows, null while
  // it loads, for a list of gates or the Outbox), and its body, drawn in the view's served layout; the review on
  // the review's route.
  let { tools = undefined }: { tools?: Snippet<[Lens, Pipeline | null]> } = $props();

  const route = $derived(readHash());
  const on = $derived(isCoreView(route.view));
  const review = $derived(route.view === REVIEW);
  const spec = $derived(viewSpec(route.view));
  const label = $derived(viewLabel(route.view));
  const loaded = $derived(tasksState.key === viewQueryKey(route.view, route.area, route.ctx));
  const n = $derived(loaded ? tasksState.tasks.length : 0);
  const lens = $derived(lensOf(route));
  const outbox = $derived(!!spec && spec.layout === OUTBOX_LAYOUT);
  const gates = $derived(!!spec && spec.over === "gates");
  const res = $derived(on && !review && loaded && !outbox && !gates ? pipelineOf(taskLensItems(), lens, null) : null);
  const areas = $derived(on && !review ? areaOptions(route) : []);
  const chips = $derived(on && !review ? contextChips(route) : []);
  const now = $derived(minute());
  const model = (it: J, copy: string) => taskModel(it.task, route.task, copy, lens, now);

  $effect(() => {
    if (route.view !== REVIEW) reviewState.step = "";
  });
</script>

<section id="tasks" class="tasks" hidden={!on} aria-label={on ? (review ? reviewTitle() : label) : undefined}>
  <div id="tasks-head">
    {#if on && !review}
      <div class="tools tasks-tools">
        <h2 class="tasks-head">{label}</h2>
        {#if areas.length}
          <label class="lens-pick"
            ><span class="lens-label">{t("Area")}</span><select data-focus="view:area" onchange={(ev) => writeHash({ area: ev.currentTarget.value })}
              ><option value="" selected={!route.area}>{t("All areas")}</option>{#each areas as a (a.value)}<option value={a.value} selected={route.area === a.value}>{a.label}</option
                >{/each}</select
            ></label
          >
        {/if}
        {#each chips as c (c.ctx)}<button
            type="button"
            class={"chip" + (c.on ? " on" : "")}
            aria-pressed={c.on ? "true" : "false"}
            data-focus={"view:ctx:" + c.ctx}
            onclick={() => writeHash({ ctx: c.next })}>{c.ctx}{c.count}</button
          >{/each}
        {#if spec && spec.remove_clean}<button
            type="button"
            class="act"
            data-focus="tasks:remove-clean"
            disabled={n === 0 || removeState.busy}
            title={t("Remove the workspaces of this view's tasks whose checkouts hold nothing unpublished")}
            onclick={() => removeAllClean(n, route.view)}>{t("Remove all clean")}</button
          >{/if}
      </div>
      <ViewIntro {spec} />
    {/if}
  </div>
  {#if tools && on && !review}{@render tools(lens, res)}{/if}
  <div id="tasks-body">
    {#if on && review}
      <Review open={route.task} />
    {:else if on}
      {#if tasksState.error}<p class="plan-error">{tasksState.error}</p>{/if}
      {#if !loaded}
        <p class="empty">{t("Loading.")}</p>
      {:else}
        {#each tasksState.loading as l (l.plugin)}<p class="loading" data-plugin={l.plugin}
            >{l.reason ? t("Loading {plugin}: {reason}", { plugin: l.plugin, reason: l.reason }) : t("Loading {plugin}", { plugin: l.plugin })}</p
          >{/each}
        {#if outbox}
          {#if outboxState.error}<p class="plan-error">{outboxState.error}</p>{/if}
          {#if ((outboxState.page && outboxState.page.groups) || []).length}
            <Outbox page={outboxState.page} />
          {:else}
            <p class="empty">{outboxState.page ? spec.empty : t("Loading.")}</p>
          {/if}
        {:else if gates}
          {#if tasksState.gates.length}
            <div class="gate-list">
              {#each tasksState.gates as g (g.id)}<GateCard task={taskById(g.task, detailState.task, detailState.unitTasks)} gate={g} />{/each}
            </div>
          {:else}
            <p class="empty">{spec.empty}</p>
          {/if}
        {:else if !tasksState.tasks.length}
          <p class="empty">{spec && spec.empty}</p>
        {:else if res && !res.shown}
          <p class="empty">{t("Nothing here matches \"{query}\".", { query: route.q })}</p>
        {:else if res && lens.layout === "board"}
          <Board groups={res.groups.filter((g) => g.rows.length)} {lens} title={viewLabel(lens.view)} {model} class="board task-board" />
        {:else if res}
          <RowList {res} {model} {lens} cls="task-list" />
        {/if}
      {/if}
    {/if}
  </div>
</section>
