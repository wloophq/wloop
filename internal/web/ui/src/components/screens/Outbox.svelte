<script lang="ts">
  import { t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { outboxState } from "../../state/store.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import { disableWorkflow } from "./outbox";

  // The Outbox: the drafts waiting on you, grouped by why they were drafted, as GET /api/outbox serves them.
  let { page }: { page: J } = $props();

  const groups = $derived((page && page.groups) || []);
  const groupKey = (g: J, i: number) => g.workflow || "group:" + (g.purpose || "") + ":" + i;
</script>

<div class="outbox">
  {#each groups as g, i (groupKey(g, i))}
    {@const drafts = g.drafts || []}
    <section class="ob-group" data-workflow={g.workflow || ""} data-purpose={g.purpose || ""} aria-label={g.title}>
      <div class="ob-head">
        <h3 class="ob-title">{g.title}</h3>
        <span class="ob-n">{tn("{n} draft", drafts.length)}</span>
        {#if g.workflow}
          <code class="ob-workflow">{g.workflow}</code>
          {#if outboxState.confirming !== g.workflow}
            <span class="ob-stop"
              ><button type="button" class="act" data-focus={`outbox:stop:${g.workflow}`} onclick={() => (outboxState.confirming = g.workflow)}>{t("Stop drafting these")}</button
              ></span>
          {:else}
            <span class="ob-stop" role="group" aria-label={t("Stop drafting these")}
              ><button type="button" class="act primary" data-focus={`outbox:stop-confirm:${g.workflow}`} onclick={() => disableWorkflow(g.workflow)}>{t("Stop this workflow")}</button
              ><button type="button" class="act" data-focus={`outbox:stop-cancel:${g.workflow}`} onclick={() => (outboxState.confirming = "")}>{t("Keep it")}</button></span>
          {/if}
        {/if}
      </div>
      {#if g.about}<p class="ob-about">{g.about}</p>{/if}
      {#each drafts as d (d.id)}<DraftCard task={null} draft={d} scope="outbox" context={d.context} />{/each}
    </section>
  {/each}
</div>
