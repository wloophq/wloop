<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { taskState } from "../../state/store.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import Markdown from "../common/Markdown.svelte";
  import { runAct, runStateWord, runTranscript } from "./task";

  // The task's runs, each with where it ran and its session; its actions are the ones the run offers, and its
  // transcript shows under it once read.
  let { e }: { e: J } = $props();

  const RUN_BUTTONS = [
    { action: "open", label: () => t("Open workspace") },
    { action: "enter", label: () => t("Enter session") },
    { action: "transcript", label: () => t("Transcript") },
  ];
</script>

{#if (e.runs || []).length}
  <section class="d-runs" aria-label={t("Runs")}>
    {#each e.runs as r (r.seq)}
      {@const offers = new Set(r.actions || [])}
      {@const shown = taskState.transcript && taskState.transcript.seq === r.seq ? taskState.transcript : null}
      <article class="d-run" data-run={String(r.seq)}>
        <div class="meta"><strong>{r.intent}</strong>{" "}<span class="pill">{runStateWord("run_states", r.state)}</span>{#if r.manager}{" "}{t("in {manager}", { manager: r.manager })}{/if}{#if r.session}{" "}{t("session")}{" "}<code>{r.session}</code>{r.session_state ? ` (${runStateWord("session_states", r.session_state)})` : ""}{/if}</div>
        {#if r.worktree || r.workspace}<div class="meta"><code>{r.worktree || r.workspace}</code></div>{/if}
        {#if r.resume}<div class="meta">{t("Resume:")}{" "}<code>{r.resume}</code></div>{/if}
        <div class="d-actions">
          {#each RUN_BUTTONS as b (b.action)}
            <ActionButton
              slot={`run:${r.seq}:${b.action}`}
              class={"act" + (offers.has(b.action) ? "" : " off")}
              disabled={!offers.has(b.action)}
              run={() => (b.action === "transcript" ? runTranscript(r) : runAct(r, b.action, b.label()))}>{b.label()}</ActionButton>
          {/each}
        </div>
        <ActionResult slots={RUN_BUTTONS.map((b) => `run:${r.seq}:${b.action}`)} />
        {#if shown}
          {#if shown.error}<p class="plan-error">{shown.error}</p>{:else}<ol class="transcript">{#each shown.lines as l, i (i)}<li><Markdown text={l} fold={12} key={`run:${r.seq}:${i}`} /></li>{/each}</ol>{/if}
        {/if}
      </article>
    {/each}
  </section>
{/if}
