<script lang="ts">
  import { actionStates } from "../../lib/actions";
  import { t } from "../../lib/i18n.svelte";
  import type { Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import Facet from "../common/Facet.svelte";
  import TagChips from "./TagChips.svelte";
  import { RISKS, SIZES, stamp } from "./unit";

  // The unit's brief, or why it has none yet, with the served brief actions.
  let { b, r, lens = undefined }: { b: J; r: J; lens?: Lens } = $props();

  const refreshes = $derived(r ? actionStates(r, "detail", ["brief"]).length > 0 : false);
  const x = $derived(b && b.brief);
  const failed = $derived(!!b && b.state === "failed");
  const notes = $derived(
    b && x
      ? [b.model, stamp(b.at), b.stale && !failed ? t("refreshing") : "", failed ? t("last refresh failed: {error}", { error: b.error || t("unknown error") }) : ""]
          .filter(Boolean)
          .join(" · ")
      : "",
  );
  const risk = $derived(x && RISKS.includes(x.risk) ? x.risk : "");
  const size = $derived(x && SIZES.includes(x.size) ? x.size : "");
</script>

{#snippet refresh()}{#if r}<ActionButtons subject={r} surface="detail" scope="detail" groups={["brief"]} />{/if}{/snippet}

{#snippet facet(field: string, value: string, tone: string, text: string)}
  {#if lens && value}
    <Facet {lens} {field} raw={value} slot={"detail:facet:" + field}><span class={["pill", tone].filter(Boolean).join(" ")}>{text}</span></Facet>
  {:else}
    <span class={["pill", tone].filter(Boolean).join(" ")}>{text}</span>
  {/if}
{/snippet}

{#if !b}
  {#if refreshes}<div class="d-brief muted">{@render refresh()}</div>{/if}
{:else if b.state === "pending" && !b.brief}
  <div class="d-brief muted">{t("Brief pending")} {@render refresh()}</div>
{:else if !b.brief}
  <div class="d-brief muted">{b.error ? t("Brief failed: {error}. It is tried again later.", { error: b.error }) : t("Brief failed. It is tried again later.")} {@render refresh()}</div>
{:else}
  <section class="d-brief" aria-label={t("Brief")}>
    <div class="d-brief-tags">
      {@render facet("type", x.kind, "", x.kind)}
      {@render facet("size", size, "", t("size {size}", { size: x.size }))}
      {#if risk}{@render facet("risk", risk, "risk-" + risk, t("{risk} risk", { risk }))}{/if}
      <TagChips tags={Array.isArray(x.tags) ? x.tags : []} {lens} slot="detail:facet:" all />
    </div>
    <p class="goal">{x.goal}</p>
    {#if x.context}<p>{x.context}</p>{/if}
    {#if x.risk_why}<p class="muted">{t("Risk: {risk}", { risk: x.risk_why })}</p>{/if}
    {#if (x.open_questions || []).length}
      <ul class="questions">
        {#each x.open_questions as q, i (i)}<li>{q}</li>{/each}
      </ul>
    {/if}
    <div class="muted small">{notes} {@render refresh()}</div>
  </section>
{/if}
