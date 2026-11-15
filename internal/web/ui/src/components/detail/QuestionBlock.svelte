<script lang="ts">
  import { t, tn } from "../../lib/i18n.svelte";
  import { keyOf, type J } from "../../lib/util";
  import ActionResult from "../actions/ActionResult.svelte";
  import GuidanceBox from "./GuidanceBox.svelte";
  import TurnButton from "./TurnButton.svelte";
  import { ARTIFACT_LINES, answerNote, artifactOf, guidanceLabel, questionChoices, questionPins, questionRecord } from "./unit";

  // The turn that waits on the user: the agent's question with its options, its stop with the last message, or
  // its queued answer.
  let { d }: { d: J } = $props();

  const r = $derived(d.row);
  const q = $derived(r.question);
  const rec = $derived(questionRecord(d));
  const asked = $derived(q.state === "asked" && !q.queued);
  const head = $derived(q.queued ? t("Your answer is queued") : asked ? t("Agent asks") : t("Agent stopped"));
  const last = $derived(q.last_message || (d.turn && d.turn.last_message) || "");
  const choices = $derived(asked ? questionChoices(r, q) : []);
  const artifact = $derived(artifactOf(rec.artifact));
</script>

<section class="d-question" aria-label={head}>
  <h2 class="d-sub">{head}</h2>
  {#if q.queued}<p class="muted small">{t("The agent gets it once its session is free. An answer sent now replaces it.")}</p>{/if}
  {#if rec.summary}<p class="summary">{rec.summary}</p>{/if}
  {#if q.verdict}<p class="verdict">{t("drafted {verdict}, {blocking} blocking, {nonBlocking} non-blocking", { verdict: q.verdict, blocking: q.blocking, nonBlocking: q.non_blocking })}</p>{/if}
  {#if asked && q.text}<pre class="q-text">{q.text}</pre>{/if}
  {#if !asked && !q.queued && last}<div class="muted small">{t("Last message")}</div><pre class="q-text">{last}</pre>{/if}
  {#if asked && q.kind === "free" && q.recommend}<p class="recommend">{t("Agent recommends: {answer}", { answer: q.recommend })}</p>{/if}
  {#if choices.length}
    <div class="q-options">
      {#each choices as c (c.slot)}
        <TurnButton row={r} slot={c.slot} label={c.label} name="answer" extra={c.extra} primary={c.rec} rec={c.rec} title={c.title} />
      {/each}
    </div>
  {/if}
  <ActionResult slots={choices.map((c) => `detail:${c.slot}`)} />
  {#if asked && questionPins(q)}<p class="d-note">{t("An answer approves a draft: open the question's task to see it before you answer.")}</p>{/if}
  <div class="q-answer">
    <label class="d-sub" for="guidance">{guidanceLabel(r)}</label>
    <GuidanceBox key={keyOf(r)} placeholder={choices.length ? t("Ask for something else, or add a note to the choice above") : t("Tell the agent what to do next")} />
    <div class="q-send">
      <TurnButton
        row={r}
        slot="answer"
        label={t("Send answer")}
        name="answer"
        title={t("Send the text as your answer: typed in the agent's session when it is there, else a new session resumes the turn with it")}
        disabled={!answerNote(r)} />
    </div>
  </div>
  <div class="q-turn">
    <TurnButton row={r} slot="continue" label={t("Continue")} name="continue" title={t("The agent goes on with the turn without an answer")} />
    <TurnButton row={r} slot="dismiss" label={t("Dismiss")} name="dismiss" title={t("Close the question; the pull request goes back to whoever it waits on")} />
  </div>
  {#if artifact}
    {#if artifact.lines <= ARTIFACT_LINES}
      <section class="d-artifact" aria-label={artifact.kind}><div class="d-sub">{artifact.kind}</div><pre class="artifact-body">{artifact.text}</pre></section>
    {:else}
      <details class="d-artifact"><summary class="d-sub">{tn("{kind}, {n} lines", artifact.lines, { kind: artifact.kind })}</summary><pre class="artifact-body">{artifact.text}</pre></details>
    {/if}
  {/if}
</section>
