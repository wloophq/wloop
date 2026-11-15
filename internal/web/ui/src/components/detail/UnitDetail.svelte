<script lang="ts">
  import { actionNotes, actionStates, focusSlot } from "../../lib/actions";
  import { providerName, rowKindLabel, eventLabel } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { isMuted, pl, reasonText, sm, waitClass } from "../../lib/lens";
  import { readLens } from "../../lib/view";
  import { isSet, keyOf, labelOf, safeURL, unitOf, type J } from "../../lib/util";
  import { act } from "../../state/actions.svelte";
  import { state } from "../../state/store.svelte";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import DetailWindowControls from "../window/DetailWindowControls.svelte";
  import BriefBlock from "./BriefBlock.svelte";
  import GuidanceBox from "./GuidanceBox.svelte";
  import QuestionBlock from "./QuestionBlock.svelte";
  import UnitExtras from "./UnitExtras.svelte";
  import {
    age, answerNote, closeDetail, countdownOf, detailFacts, detailOf, guidanceLabel, intakeLines, intakeWaiting, roundGate, stamp,
    timelineEvents, trimmedBlocks, waitingLabel,
  } from "./unit";

  // The inside of the detail panel for the unit the route opens; nothing when it opens none.
  const d = $derived(detailOf());

  // revealOnOpen brings the panel into view when a unit opens on a narrow screen, where it stacks under the list.
  function revealOnOpen(node: HTMLElement) {
    if (typeof matchMedia === "function" && matchMedia("(max-width: 900px)").matches) (node.closest("#detail") || node).scrollIntoView({ block: "start" });
  }
</script>

{#if d}
  {#key keyOf(d.row)}
    {@const r = d.row}
    {@const p = pl(r)}
    {@const o = p.override || {}}
    {@const inCard = !!r.question}
    {@const ws = p.workspace || {}}
    {@const muteState = (p.mute || {}).until_state_change ? t("muted until who it waits on changes") : isMuted(p) ? t("muted until {time}", { time: stamp(p.mute.until) }) : ""}
    {@const trimmed = trimmedBlocks(d)}
    <div class="d-top" {@attach revealOnOpen}>
      <div class="d-head">
        <button type="button" class="close" aria-label={t("Close detail")} data-focus="detail:close" onclick={closeDetail}>×</button>
        <DetailWindowControls />
        <h1>{r.title || labelOf(r)}</h1>
        <div class="meta">
          <span class="ref">{labelOf(r)}</span>{" "}<a href={safeURL(r.link)} target="_blank" rel="noopener noreferrer" class="ext" data-focus="detail:link">{providerName(unitOf(r).provider)}</a>{#if p.author}{" "}{t("by")}{" "}<span class="who">{p.author}</span>{/if}
        </div>
      </div>
      <div class={"d-sit " + waitClass(r) + (sm(r).stalled ? " late" : "")}>
        <div class="d-clock">
          {#if roundGate(r)}
            {@const c = countdownOf(r)}
            <span class="age countdown" data-deadline={c.deadline} data-provider={c.provider} title={c.title}>{c.text}</span>
          {:else}
            <span class="age">{age(sm(r).since)}</span>
          {/if}<span class="pill">{rowKindLabel(r)}</span>
        </div>
        <div>{t("Waiting on")} <strong>{waitingLabel(r)}</strong>{isSet(sm(r).since) ? " " + t("since {time}", { time: stamp(sm(r).since) }) : ""}</div>
        <div class="why">{reasonText(r)}</div>
        {#if sm(r).line && sm(r).line !== sm(r).reason}<div class="line">{sm(r).line}</div>{/if}
        {#if sm(r).stalled}<div class="late-note">{t("Past its threshold")}</div>{/if}
      </div>
      {#if r.kind === "checks" && (state.checks[keyOf(r)] || []).length}
        <section class="d-checks" aria-label={t("Failing checks")}>
          <h2 class="d-sub">{t("Failing checks")}</h2>
          <ul>
            {#each state.checks[keyOf(r)] as c, i (c.name + "@" + i)}
              <li>{#if safeURL(c.url)}<a href={safeURL(c.url)} target="_blank" rel="noopener noreferrer">{c.name}</a>{:else}{c.name}{/if}{#if c.summary}<span class="muted">{` · ${c.summary}`}</span>{/if}{#if (c.steps || []).length}<span class="muted">{" · " + t("failed at {steps}", { steps: c.steps.join(", ") })}</span>{/if}</li>
            {/each}
          </ul>
        </section>
      {/if}
      <dl class="d-facts">
        <dt>{t("In scope as")}</dt><dd>{(p.reasons || []).join(", ") || t("out of scope")}</dd><dt>{t("Workspace")}</dt><dd>{#if ws.display_name}<span class="ws">{ws.display_name}</span>{#if ws.status}<span class="col">{ws.status}</span>{/if}{#if (ws.agent_states || []).length}<span class="agent">{ws.agent_states.join(", ")}</span>{/if}<a href={safeURL(r.link)} target="_blank" rel="noopener noreferrer" class="ext" data-focus="detail:link" onclick={(e) => e.stopPropagation()}>{providerName(unitOf(r).provider)}</a>{:else}{t("none")}{/if}</dd><dt>{t("Controls")}</dt><dd>{[o.hold ? t("paused") : "", muteState, o.adopted ? t("adopted") : ""].filter(Boolean).join(", ") || t("none")}</dd>{#each detailFacts(r, d, o) as f (f.field)}<dt title={f.title}>{f.label}</dt><dd>{f.value}</dd>{/each}
      </dl>
      {#if inCard}<QuestionBlock {d} />{/if}
      <UnitExtras {d} />
      {#if trimmed}<div class="d-trimmed muted small">{t("Guide trimmed over the judgment limit: {blocks}", { blocks: trimmed })}</div>{/if}
      {#if intakeWaiting(r)}
        <section class="d-intake" aria-label={t("Briefing")}>
          <h2 class="d-sub">{t("Briefing")}</h2>
          {#each intakeLines(p.intake) as l, i (i)}<div>{l}</div>{/each}
          <div class="q-turn"><ActionButtons subject={r} surface="detail" scope="detail" groups={["intake"]} /></div>
        </section>
      {/if}
      {#if !inCard}<label class="d-sub" for="guidance">{guidanceLabel(r)}</label>{/if}
    </div>
    {#if !inCard}<GuidanceBox key={keyOf(r)} placeholder={guidanceLabel(r)} />{/if}
    <div class="d-acts">
      <div class="d-actions"><ActionButtons subject={r} surface="detail" scope="detail" run={(id) => act(r, id, { guidance: answerNote(r) })} /></div>
      <ActionResult slots={actionStates(r, "detail").map(([st]) => `detail:${focusSlot(st.id)}`)} />
      {#each actionNotes(r, "detail") as n, i (i)}<p class="act-note">{n}</p>{/each}
      <BriefBlock b={d.brief} {r} lens={readLens()} />
    </div>
    <div class="d-log">
      <h2 class="d-sub">{t("Timeline")}</h2>
      <ol class="timeline">
        {#each timelineEvents(d) as e (e.id)}
          <li class={"ev-" + e.kind}><time datetime={e.at}>{stamp(e.at)}</time><div><span class="kind">{eventLabel(e.kind)}</span>{#if e.actor}<span class="who">{" " + e.actor}</span>{/if}</div>{#if e.data}<span class="data">{e.data}</span>{/if}</li>
        {/each}
      </ol>
    </div>
  {/key}
{/if}
