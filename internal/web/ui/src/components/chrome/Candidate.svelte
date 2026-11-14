<script lang="ts">
  import { brand } from "../../brand";
  import { reviewDecision, rowKindLabel } from "../../lib/catalog";
  import { t, tn } from "../../lib/i18n.svelte";
  import { pl, rowByKey, sm, waitClass } from "../../lib/lens";
  import { keyOf, labelOf, type J } from "../../lib/util";
  import { writeHash } from "../../state/route.svelte";
  import { state } from "../../state/store.svelte";
  import BriefTags from "./BriefTags.svelte";
  import { candidateWait } from "./explore";
  import { age, ago, waitingLabel } from "./facts";

  // An Explore candidate: its title and when it moved, its facts, whether the loop holds it, its brief and how the
  // last action on it went. A click selects it for the actions under the search.
  let { c, sel }: { c: J; sel: string } = $props();

  const key = $derived(keyOf(c));
  const row = $derived(rowByKey(key));
  const facts = $derived.by(() => {
    const out = [
      `+${c.additions} -${c.deletions} · ` + tn("{n} files", c.changed_files),
      reviewDecision(c.review_decision),
      c.checks ? t("checks {state}", { state: c.checks.toLowerCase() }) : t("no checks"),
      tn("{n} comments", c.comments),
    ];
    if ((c.review_requests || []).length) out.push(t("asks {who}", { who: c.review_requests.map((w: string) => w.replace(/^team:/, "")).join(", ") }));
    if (c.my_review) {
      const review = c.my_review.toLowerCase().replace("_", " ");
      out.push(c.my_review_on_head ? t("my review: {review}", { review }) : t("my review: {review} (older head)", { review }));
    }
    return out.join(" · ");
  });
  const b = $derived(c.brief && c.brief.brief);
  const wait = $derived(candidateWait(c, row, state.health && state.health.stalled_review_hours));
  const st = $derived(state.explore.status[key]);
</script>

<li class={["cand", row ? waitClass(row) : "", key === sel ? "sel" : ""].join(" ")}>
  <button type="button" class="cand-pick" aria-pressed={(key === sel) ? "true" : "false"} data-focus={`cand:${key}`} onclick={() => (state.explore.sel = state.explore.sel === key ? "" : key)}
    ><span class="title">{c.title || labelOf(c)}</span><span class="meta"
      >{t("{unit} by {author} · opened {opened} · updated {updated}", { unit: labelOf(c), author: c.author, opened: ago(c.created_at), updated: ago(c.updated_at) })}{#if wait}<span
          class={"wait" + (wait.late ? " late" : "")}
          title={row ? t("waiting on {who}", { who: waitingLabel(row) }) : t("since the last activity")}>{wait.text}</span
        >{/if}{#if c.draft}<span class="flag">{t("draft")}</span>{/if}{#if c.state && c.state !== "OPEN"}<span class="flag">{c.state.toLowerCase()}</span>{/if}</span
    ></button
  >
  <div class="facts">{facts}</div>
  {#if row}
    <div class="inv">
      <span class="pill">{rowKindLabel(row)}</span>{" " +
        t("waiting on {who} for {age} · in {name} as {reasons}", {
          name: brand.name, who: waitingLabel(row), age: age(sm(row).since), reasons: (pl(row).reasons || []).join(", ") || t("out of scope"),
        })}
    </div>
  {:else}
    <div class="inv">{t("not in {name}", { name: brand.name })}</div>
  {/if}
  {#if b}
    <div class="brief-line"><BriefTags b={{ size: b.size, risk: b.risk, open_questions: (b.open_questions || []).length }} />{` ${b.kind} · ${b.goal}`}</div>
  {/if}
  {#if st}
    <div class={["inv", "cand-status", st.error ? "err" : ""].filter(Boolean).join(" ")} role="status">
      {st.text}{#if st.done && row}<button type="button" class="act" data-focus={`cand:open:${key}`} onclick={() => writeHash({ pr: key })}
          >{t("Open in {name}", { name: brand.name })}</button
        >{/if}
    </div>
  {/if}
</li>
