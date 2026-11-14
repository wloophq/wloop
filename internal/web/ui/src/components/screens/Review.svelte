<script lang="ts">
  import { viewSpec, REVIEW } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { lensOf } from "../../lib/lens";
  import { taskItemOf } from "../../lib/tasks";
  import type { J } from "../../lib/util";
  import { minute } from "../../state/clock.svelte";
  import { reviewCurrent, reviewSteps } from "../../state/loaders.svelte";
  import { reviewState, tasksState } from "../../state/store.svelte";
  import RowList from "../rows/RowList.svelte";
  import { taskModel } from "../rows/model";
  import ViewIntro from "./ViewIntro.svelte";
  import { pickStep, reviewKey, reviewTitle } from "./review";

  // The review: its steps with their counts, then the step shown with its tasks. A step lists what its view holds,
  // so its pieces filter nothing.
  let { open = "" }: { open?: string } = $props();

  const steps = $derived(reviewSteps());
  const step = $derived(reviewState.review ? reviewCurrent() : null);
  const lens = $derived(step ? { ...lensOf({ view: step.view, q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false }), static: true } : null);
  const res = $derived({ groups: [{ field: "", key: "", raw: "", label: "", rows: tasksState.tasks.map(taskItemOf) }], group: "" });
  const now = $derived(minute());
  const model = (it: J, copy: string) => taskModel(it.task, open, copy, lens!, now);
</script>

<div class="tools tasks-tools"><h2 class="tasks-head">{reviewTitle()}</h2></div>
<ViewIntro spec={viewSpec(REVIEW)} />
{#if reviewState.error}<p class="plan-error">{reviewState.error}</p>{/if}
{#if !reviewState.review}
  <p class="empty">{t("Loading.")}</p>
{:else}
  <ol class="review-steps" aria-label={t("Review steps")}>
    {#each steps as s (s.id)}
      {@const on = s.id === reviewState.step}
      <li class={"review-step" + (on ? " sel" : "") + (s.done ? " done" : "")}>
        <button type="button" class="review-pick" aria-current={on ? "step" : "false"} data-focus={"review:pick:" + s.id} onclick={() => pickStep(s.id)}
          ><span class="review-mark" aria-hidden="true">{s.done ? "✓" : "○"}</span><span class="review-label">{s.label}</span><span class="count">{String(s.count)}</span></button
        >
      </li>
    {/each}
  </ol>
  {#if reviewState.review.done}<p class="empty review-done">{t("Review done. A new one starts the next time you open it.")}</p>{/if}
  {#if step}
    <div class="review-head">
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <h3 class="plan-head" tabindex="0" data-focus={"review:step:" + step.id} title={t("Enter marks the step done; ] and [ move between steps")}>{`${step.label} (${step.count})`}{step.done ? " ✓" : ""}</h3>
      {#if !step.done}<button type="button" class="act" data-focus={"review:done:" + step.id} onclick={() => reviewKey("done")}>{t("Mark done")}</button>{/if}
    </div>
    {#if tasksState.error}<p class="plan-error">{tasksState.error}</p>{/if}
    {#if tasksState.key !== "review|" + step.id}
      <p class="empty">{t("Loading.")}</p>
    {:else if !tasksState.tasks.length}
      <p class="empty">{(viewSpec(step.view) || {}).empty}</p>
    {:else if lens}
      <RowList {res} {model} {lens} cls="task-list" />
    {/if}
  {/if}
{/if}
