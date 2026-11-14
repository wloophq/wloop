<script lang="ts">
  import { onMount } from "svelte";
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { claimCopy } from "../../state/actions.svelte";
  import { askConfirm, type PaletteItem } from "../../state/overlays.svelte";
  import { win } from "../../state/route.svelte";
  import { scheduleLoad } from "../../state/sync.svelte";
  import Markdown from "../common/Markdown.svelte";
  import {
    OMNI_STOPS, omniboxActed, omniboxAct, omniboxActiveID, omniboxChoices, omniboxEdits, omniboxFollow, omniboxIsOption, omniboxKey, omniboxNew,
    omniboxOpLabel, omniboxOptionID, omniboxRun, omniboxSelected, omniboxStatus, omniboxTrap, omniboxType, omniboxUnitAction, omniFetch, settleCopy,
    type OmniEnv,
  } from "./omnibox";

  // The omnibox over the page, or, in the floating panel, the whole page. Its state lives as long as it is open;
  // env reaches the daemon and the page, and a test may pass its own.
  let {
    panel = false,
    items = [],
    text = "",
    onclose,
    env: given,
  }: { panel?: boolean; items?: PaletteItem[]; text?: string; onclose: () => void; env?: Partial<OmniEnv> } = $props();

  // svelte-ignore state_referenced_locally
  const st = $state(omniboxNew(panel, items));
  const back = typeof document === "undefined" ? null : (document.activeElement as HTMLElement | null);
  let input: HTMLInputElement;
  let box: HTMLDivElement;
  let closed = false;

  // close leaves the omnibox: the focus goes back where it was before it opened.
  function close() {
    if (closed) return;
    closed = true;
    if (!panel && back && back.isConnected && back.focus) back.focus();
    onclose();
  }

  const env: OmniEnv = {
    get: (url, signal) => omniFetch(url, undefined, signal),
    post: (url, body, signal) => omniFetch(url, body, signal),
    later: (fn, ms) => setTimeout(fn, ms),
    clear: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
    now: () => performance.now(),
    open: (href) => {
      win.location().hash = href;
      if (!panel) close();
    },
    act: (u, id, copies) => omniboxAct(u, id, copies),
    claim: () => claimCopy(),
    settle: (name, out, claim) => settleCopy(name, out, claim),
    confirm: (q) => askConfirm(q),
    close: () => close(),
    changed: () => scheduleLoad({ tasks: true }),
    // svelte-ignore state_referenced_locally
    ...given,
  };

  const agent = $derived(st.asking || !!st.answer || !!st.askError);
  const choices = $derived(omniboxChoices(st, env));
  const selected = $derived(omniboxSelected(st, choices));
  const activeID = $derived(omniboxActiveID(st, env));
  const resolved = $derived(st.resolved);
  const acted = $derived(omniboxActed(st));
  const checkedCount = $derived(st.checked.filter(Boolean).length);
  const editable = $derived(!!(st.answer && st.answer.proposal));

  // opt are the attributes that make the element of choice id an option, selected or not; a choice that is no
  // option is marked with data-selected.
  function opt(id: string, option = false): Record<string, string> {
    const i = choices.findIndex((c) => c.id === id);
    if (i < 0) return {};
    const at = { id: omniboxOptionID(i), "data-choice": id };
    return option ? { ...at, role: "option", "aria-selected": String(id === selected) } : { ...at, "data-selected": String(id === selected) };
  }

  function taskHref(id: string): string {
    return "#task=" + encodeURIComponent(id);
  }

  function queueHref(tasks: J[]): string {
    return "#queue=list&ids=" + tasks.map((e) => encodeURIComponent(e.id)).join(",");
  }

  function pick(it: PaletteItem) {
    env.close();
    it.run();
  }

  function boxKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    const stops = [...box.querySelectorAll<HTMLElement>(OMNI_STOPS)].filter((el) => !(el as HTMLButtonElement).disabled);
    const to = omniboxTrap(e, stops, document.activeElement);
    if (to) {
      e.preventDefault();
      to.focus();
    }
  }

  function replyKey(e: KeyboardEvent) {
    if (e.key !== "Enter") return;
    e.preventDefault();
    e.stopPropagation();
    void omniboxFollow(st, env);
  }

  function shadeDown(e: MouseEvent) {
    if (e.target === e.currentTarget && !(e.detail > 1)) close();
  }

  $effect(() => {
    if (!activeID) return;
    const el = document.getElementById(activeID);
    if (el && el.scrollIntoView) el.scrollIntoView({ block: "nearest" });
  });

  onMount(() => {
    input.focus();
    if (text) {
      input.value = text;
      void omniboxType(st, text, env);
    }
    return () => {
      env.clear(st.timer);
      if (st.abort) st.abort.abort();
    };
  });
</script>

{#snippet keyHint(k: string, what: string)}<kbd>{k}</kbd> {what}{/snippet}

{#snippet results()}
  <p class={"omni-badge" + (agent ? " agent" : "")}>{agent ? t("agent") : t("instant")}</p>
  {#if !st.text.trim()}
    <ul class="omni-items" role="listbox" id="omni-list" aria-label={t("Search or ask")}>
      {#each st.items as it, i (i)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <li {...opt("item:" + i, true)} class="omni-item" onclick={() => pick(it)}>{it.label}{#if it.meta}<span class="pill">{it.meta}</span>{/if}{#if it.key}<kbd>{it.key}</kbd>{/if}</li>
      {/each}
    </ul>
  {:else}
    {#if resolved && resolved.why}<p class="omni-why">{t(resolved.why, resolved.params || {})}</p>{/if}
    {#if resolved && resolved.kind === "unit" && resolved.unit}
      <section class="omni-unit" {...opt("unit")}>
        <h3>{#if resolved.unit.permalink}<a href={resolved.unit.permalink} target="_blank" rel="noopener">{resolved.unit.label}</a>{:else}{resolved.unit.label}{/if}</h3>
        <ol>
          {#each resolved.unit.tasks || [] as e (e.id)}<li><a class="omni-task" href={taskHref(e.id)}>{e.title || e.id}</a></li>{/each}
        </ol>
        <div class="d-actions">
          {#each resolved.unit.actions || [] as a (a.id)}
            <button type="button" class="act" onclick={() => omniboxUnitAction(st, env, a.id)}>{a.label || a.id}</button>
          {/each}
        </div>
        {#if acted}
          <p class="err omni-copy-error">{acted.error}</p>
          {#if acted.text}<textarea class="omni-copy" readonly rows="6" aria-label={acted.error} value={acted.text}></textarea>{/if}
        {/if}
      </section>
    {/if}
    {#if resolved && resolved.kind === "agent" && !agent}<p class="omni-hint" {...opt("ask")}>{t("Press ⌘↵ to ask the agent")}</p>{/if}
    {#if st.answer && st.answer.answer}
      <section class="omni-answer">
        <Markdown text={st.answer.answer.text || ""} fold={12} key="omni:answer" />
        {#if (st.answer.answer.tasks || []).length}
          <ol>{#each st.answer.answer.tasks as e (e.id)}<li><a class="omni-task" href={taskHref(e.id)}>{e.title || e.id}</a></li>{/each}</ol>
          <a class="omni-queue" href={queueHref(st.answer.answer.tasks)}>{t("Open as a queue")}</a>
        {/if}
      </section>
    {/if}
    {#if st.answer && st.answer.proposal}
      <div class="omni-summary"><Markdown text={st.answer.proposal.summary || ""} fold={12} key="omni:summary" /></div>
      {#if st.answer.proposal.question}
        <div class="omni-question">
          <Markdown text={st.answer.proposal.question} fold={0} />
          <input type="text" aria-label={t("Your answer")} data-focus="omni:reply" bind:value={st.reply} onkeydown={replyKey} />
        </div>
      {/if}
    {/if}
    {#if st.answer}
      {#each st.answer.dropped || [] as d, i (i)}<p class="omni-dropped">{t(d.why, d.params || {})}</p>{/each}
    {/if}
    {#if st.ops.length}
      <section class="omni-ops" aria-label={t("To confirm")}>
        <ol>
          {#each st.ops as op, i (i)}
            <li class="omni-op">
              <label><input type="checkbox" data-focus={`omni:op:${i}`} bind:checked={st.checked[i]} /> {omniboxOpLabel(op)}</label>
              {#if op.reason && op.op !== "action"}<p class="why">{op.reason}</p>{/if}
              {#if editable}
                {#each omniboxEdits(op) as f (f.name)}
                  <input type="text" class="omni-edit" aria-label={f.label} data-focus={`omni:edit:${i}:${f.name}`} bind:value={op[f.name]} />
                {/each}
              {/if}
            </li>
          {/each}
        </ol>
        <button type="button" class="act primary" disabled={checkedCount === 0} data-focus="omni:run" {...opt("ops")} onclick={() => omniboxRun(st, env)}><kbd>↵</kbd> {tn("Run {n}", checkedCount)}</button>
      </section>
    {/if}
    {#if st.runError}
      <p class="err">{st.runError}</p>
    {:else if st.ran}
      <ol class="omni-ran">
        {#each st.ran.ops as op, i (i)}
          {@const r = st.ran.results[i] || {}}
          <li class={r.ok ? "ok" : "err"}>{r.ok ? "✓ " : "✗ "}{omniboxOpLabel(op)}{#if !r.ok}<span class="why"> {t(r.error || "run_failed", r.params || {})}</span>{/if}</li>
        {/each}
      </ol>
    {/if}
    {#if st.asking}<p class="loading">{t("Asking the agent...")}</p>{/if}
    {#if st.askError}<p class="err omni-ask-error">{st.askError}</p>{/if}
    <output class="omni-status" aria-live="polite">{omniboxStatus(st, env)}</output>
    <div class="omni-list" role="listbox" id="omni-list" aria-label={t("Search or ask")}>
      {#each (st.search && st.search.groups) || [] as g (g.id)}
        <section class="omni-group" data-group={g.id} role="group" aria-label={g.label}>
          <!-- svelte-ignore a11y_hidden -->
          <h3 aria-hidden="true">{g.label} <span class="count">{fmtNumber(g.count)}</span></h3>
          <ul role="none">
            {#each g.results || [] as r (r.task.id)}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <li {...opt("task:" + r.task.id, true)} class="omni-task" data-href={taskHref(r.task.id)} onclick={() => env.open(taskHref(r.task.id))}>{r.task.title || r.task.id}{#if r.views && r.views.length}<span class="views">{r.views.join(" · ")}</span>{/if}</li>
            {/each}
          </ul>
        </section>
      {/each}
    </div>
  {/if}
{/snippet}

{#snippet omnibox()}
  <div bind:this={box} class="pal omni" class:omni-panel={panel} role="dialog" aria-modal="true" aria-label={t("Search or ask")} onkeydowncapture={boxKey}>
    <input bind:this={input} type="text" class="omni-input" role="combobox" aria-label={t("Search or ask")} aria-expanded="true" autocomplete="off" spellcheck="false"
      placeholder={t("Search, paste a link, or ask")} aria-controls="omni-list" aria-autocomplete="list" aria-activedescendant={activeID || undefined}
      oninput={() => omniboxType(st, input.value, env)} onkeydown={(e) => omniboxKey(st, env, e, () => close())} />
    <div class="omni-results">{@render results()}</div>
    <p class="omni-keys">{@render keyHint("↑↓", t("to navigate"))} · {@render keyHint("↵", t("to open"))} · {@render keyHint("⌘↵", t("to ask the agent"))} · {@render keyHint("esc", t("to close"))}</p>
  </div>
{/snippet}

{#if panel}
  {@render omnibox()}
{:else}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="pal-back" onmousedown={shadeDown}>{@render omnibox()}</div>
{/if}
