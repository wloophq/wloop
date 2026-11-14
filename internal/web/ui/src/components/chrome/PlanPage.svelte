<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { clipboard, setNotice } from "../../state/actions.svelte";
  import { state } from "../../state/store.svelte";
  import { age, stamp } from "./facts";
  import { planGroups, planHead } from "./plan";
  import PlanItem from "./PlanItem.svelte";

  // The workflows' plan: what waits on you by decision, pending first, then the sessions closed in the last day.
  let { shown = true }: { shown?: boolean } = $props();

  const p = $derived(state.plan);
  const groups = $derived(planGroups(p.items));

  function copyResume(c: J) {
    clipboard.writeText(c.resume).then(
      () => setNotice(t("copied the resume line of {session}", { session: c.session })),
      (err: Error) => setNotice(t("could not copy the resume line of {session}: {error}", { session: c.session, error: err.message }), true),
    );
  }
</script>

<section id="plan" class="plan" aria-label={t("The workflows' plan")} hidden={!shown}>
  {#if shown}
    {#if p.error}<p class="plan-error">{p.error}</p>{/if}
    {#if !groups.length}<p class="empty">{p.loaded ? t("Nothing pending or blocked in the plan.") : t("Loading the plan.")}</p>{/if}
    {#each groups as g (g.decision)}
      <h2 class="plan-head">{`${planHead(g.decision)} (${g.items.length})`}</h2>
      <ol class="plan-items">
        {#each g.items as it (it.id)}<PlanItem {it} />{/each}
      </ol>
    {/each}
    {#if p.closed.length}
      <h2 class="plan-head">{t("Sessions closed in the last day ({n})", { n: p.closed.length })}</h2>
      <ol class="plan-items">
        {#each p.closed as c (c.session + "@" + c.at)}
          <li class="plan-item closed">
            <div class="plan-line">
              <time datetime={c.at} title={stamp(c.at)}>{age(c.at)}</time>
              <span class="plan-unit">{c.session}</span>
              <span class="plan-reason">{c.reason}</span>
            </div>
            {#if c.resume}
              <div class="plan-resume">
                <code>{c.resume}</code>
                <button type="button" class="act" data-focus={`plan:closed:${c.session}`} title={t("Copy the command that resumes this session")} onclick={() => copyResume(c)}
                  >{t("Copy")}</button
                >
              </div>
            {/if}
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
</section>
