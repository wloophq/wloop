<script lang="ts">
  import { outboxHead, outboxLensItems } from "../../lib/drafts";
  import { t, tn } from "../../lib/i18n.svelte";
  import { pipelineOf, type Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { outboxState } from "../../state/store.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import { disableWorkflow } from "./outbox";

  // The Outbox's drafts through lens: filtered, sorted and grouped as a task view's tasks are, each group under its
  // value's word, with what the page's head for it says: why its drafts were written and, for a workflow's, the
  // inline confirm that stops it.
  let { page, lens }: { page: J; lens: Lens } = $props();

  const res = $derived(pipelineOf(outboxLensItems(page), lens, null));
  const groups = $derived(
    res.groups
      .filter((g) => g.rows.length)
      .map((g) => {
        const head = res.group ? outboxHead(page, res.group, g.raw) : null;
        return { key: g.key, title: g.label, about: (head && head.about) || "", workflow: (head && head.workflow) || "", drafts: g.rows.map((it) => it.draft) };
      }),
  );
</script>

<div class="outbox">
  {#each groups as g (g.key)}
    <section class="ob-group" data-workflow={g.workflow} aria-label={g.title || undefined}>
      {#if res.group}
        <div class="ob-head">
          <h3 class="ob-title">{g.title}</h3>
          <span class="ob-n">{tn("{n} draft", g.drafts.length)}</span>
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
      {/if}
      {#each g.drafts as d (d.id)}<DraftCard task={null} draft={d} scope="outbox" context={d.context} />{/each}
    </section>
  {/each}
</div>
