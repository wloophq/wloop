<script lang="ts">
import { propText, taskFacetOf } from "../../state/verbs";
  import { fmtDate, t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import Markdown from "../common/Markdown.svelte";
  import { briefData } from "./task";

  // The task's brief: its goal, text, context, risk, open questions and tags; the raw record when it holds none
  // of them.
  let { e }: { e: J } = $props();

  const r = $derived(e.brief);
  const b = $derived(briefData(e));
  const known = $derived(b.text || b.goal || b.context || b.risk || (b.open_questions || []).length || (b.tags || []).length);
</script>

{#if r}
  <section class="d-brief" aria-label={t("Brief")}>
    {#if b.goal}<p class="goal">{b.goal}</p>{/if}
    {#if b.text}<Markdown text={b.text} fold={8} key={"brief:" + e.id} />{/if}
    {#if b.context}<p>{b.context}</p>{/if}
    {#if b.risk}<p class={"risk-" + b.risk}>{t("Risk: {risk}", { risk: propText(b.risk, taskFacetOf("brief.risk")) })}{b.risk_why ? ` (${b.risk_why})` : ""}</p>{/if}
    {#if (b.open_questions || []).length}<ul>{#each b.open_questions as q, i (i)}<li>{q}</li>{/each}</ul>{/if}
    {#if (b.tags || []).length}<div class="d-brief-tags">{#each b.tags as x, i (i)}<span class="pill">{x}</span>{/each}</div>{/if}
    {#if !known}<pre class="draft-text">{JSON.stringify(r.data, null, 2)}</pre>{/if}
    {#if r.at}<div class="meta"><time datetime={r.at}>{fmtDate(r.at, "datetime")}</time></div>{/if}
  </section>
{/if}
