<script lang="ts">
  import { actionTitle } from "../../lib/actions";
  import { t } from "../../lib/i18n.svelte";
  import { safeURL, type J } from "../../lib/util";
  import { busy, state } from "../../state/store.svelte";
  import { planReason } from "../gates/gates";
  import PlanDoes from "../gates/PlanDoes.svelte";
  import PlanTarget from "../gates/PlanTarget.svelte";
  import PlanWhy from "../gates/PlanWhy.svelte";
  import { stamp } from "./facts";
  import { actOnPlan, itemLabel, planWhen } from "./plan";

  // A plan item as the plan lists it: when it runs, what its workflow does, on what, what applying it does, why it
  // fired, folded, and its actions.
  let { it }: { it: J } = $props();

  const ticking = $derived(it.decision === "pending" && it.default === "run" && !!it.deadline);
  const when = $derived(planWhen(it));
  const reason = $derived(it.reason ? planReason(it) : "");
  const link = $derived(safeURL(it.link));
  const buttons = $derived(
    (it.actions || []).map((st: J) => [st, state.planSpecs[st.id]] as [J, J]).filter(([, sp]: [J, J]) => sp && sp.surfaces.includes("detail")),
  );
</script>

<li class={`plan-item ${it.decision}`}>
  <div class="plan-line">
    <span class={"plan-when" + (ticking ? " plan-left" : "")} data-deadline={ticking ? it.deadline : undefined} title={ticking ? t("runs at {time}", { time: stamp(it.deadline) }) : undefined}
      >{when}</span
    >
    <span class="plan-wf" title={it.summary ? it.workflow : undefined}>{it.summary || it.workflow}</span>
    {#if !it.target}
      <span class="plan-unit">{#if link}<a href={link} target="_blank" rel="noopener noreferrer">{itemLabel(it)}</a>{:else}{itemLabel(it)}{/if}</span>
      {#if it.title}<span class="plan-title">{it.title}</span>{/if}
    {/if}
    {#if it.observe}
      <span class="plan-mode" title={t("This workflow observes: it only plans, and nothing runs until you apply an item or turn observe off")}>{t("observe")}</span>
    {/if}
  </div>
  {#if it.target}<PlanTarget item={it} />{/if}
  <PlanDoes item={it} />
  {#if reason && reason !== when}<p class="plan-reason">{reason}</p>{/if}
  {#if it.error}<p class="plan-error">{t("attempt {n}: {error}", { n: it.attempts, error: it.error })}</p>{/if}
  <PlanWhy item={it} />
  <div class="d-actions">
    {#each buttons as [st, sp] (st.id)}
      <button
        type="button"
        class={["act", st.enabled && st.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" ")}
        title={actionTitle(st, sp)}
        data-focus={`plan:${it.id}:${st.id}`}
        disabled={busy.has(it.id) || !st.enabled}
        onclick={() => actOnPlan(it, st.id)}>{st.label || sp.label}</button>
    {/each}
  </div>
</li>
