<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { typing, type J } from "../../lib/util";
  import { floatState, type FloatHide } from "../../state/float.svelte";
  import { notice } from "../../state/store.svelte";
  import Agents from "./Agents.svelte";
  import { floatSize, hovered, peekLeaveMS, resetFloatSize } from "./float";
  import Peek from "./Peek.svelte";
  import Queue from "./Queue.svelte";

  // The floating widget's panel page, in a window the host places beside the tab: the panel with the agents and the
  // queue, or a peek's card of what arrived, or nothing while the window is hidden. It reports its size and mode, so
  // the window fits what it draws, and asks the host to hide the window as it closes, which it says once as it loads.
  const f = floatState();
  const open = $derived(f.mode === "panel");
  const n = $derived(f.items.length);
  const peeked: J = $derived(f.mode === "peek" ? f.peeked : null);
  const maxW = $derived(f.room.maxW || window.screen.availWidth);
  const maxH = $derived(f.room.maxH || window.screen.availHeight);

  // leaving says the peek ended and its card fades out; the page closes once it is gone, so the fade is not clipped.
  let leaving = $state(false);
  let gone: ReturnType<typeof setTimeout> | undefined;

  let root: HTMLElement | undefined = $state();
  let menu = $state(false);

  const HIDES: { mode: FloatHide; label: () => string }[] = [
    { mode: "quiet", label: () => t("Hide until something new") },
    { mode: "hour", label: () => t("Hide for 1 hour") },
    { mode: "tomorrow", label: () => t("Hide until tomorrow") },
  ];

  const report = (force = false) => {
    if (f.mode === "panel" || peeked) floatSize(root, f.mode, force);
  };

  $effect(() => {
    const peek = f.mode === "peek";
    const peeking = f.peeking;
    untrack(() => {
      clearTimeout(gone);
      leaving = false;
      if (!peek || peeking) return;
      const ms = peekLeaveMS();
      if (!ms) return f.close();
      leaving = true;
      gone = setTimeout(() => f.close(), ms);
    });
  });

  $effect(() => {
    if (!f.mode) {
      menu = false;
      resetFloatSize();
    }
  });

  $effect(() => {
    void [f.mode, f.peeked, f.items.length, f.read, maxW, maxH, menu, f.turns];
    queueMicrotask(() => report());
  });

  function hide(mode: FloatHide): void {
    menu = false;
    f.hide(mode);
  }

  // unarmed drops a click on an action of a panel that is not armed for a click yet (FloatState.clickArmed).
  function unarmed(e: MouseEvent): void {
    const t = e.target as Element | null;
    if (f.clickArmed || !t || !t.closest || !t.closest(".act")) return;
    e.preventDefault();
    e.stopPropagation();
  }

  function keydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      f.close();
      return;
    }
    if (!open || e.metaKey || e.ctrlKey || e.altKey || typing(e.target as Element)) return;
    const moves: Record<string, number> = { ArrowUp: -1, ArrowDown: 1, k: -1, j: 1 };
    if (e.key in moves) {
      e.preventDefault();
      f.move(moves[e.key]);
    } else if (e.key === "Enter" && !(e.target instanceof HTMLButtonElement)) {
      e.preventDefault();
      void f.act();
    } else if (e.key === "o") {
      e.preventDefault();
      f.open();
    } else if (e.key === "u") {
      e.preventDefault();
      f.openUnit();
    }
  }

  onMount(() => {
    document.documentElement.classList.add("float-page");
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => report());
    if (ro && root) ro.observe(root);
    f.close();
    return () => {
      if (ro) ro.disconnect();
      clearTimeout(gone);
      document.documentElement.classList.remove("float-page");
    };
  });
</script>

<svelte:window onkeydown={keydown} onblur={() => f.blur()} />

<div
  class={["fl", !f.online && "offline"]}
  data-mode={f.mode || undefined}
  data-side={f.room.side || undefined}
  style:--fl-max-w="{maxW}px"
  style:--fl-max-h="{maxH}px"
  bind:this={root}
  role="presentation"
>
  {#if peeked}
    <button type="button" class="fl-flyout" data-state={leaving ? "leaving" : "shown"} use:hovered onpointerenter={() => f.holdPeek()} onpointerleave={() => f.releasePeek()} onclick={() => f.openPanel(f.peekKey)}><span class="fl-flyout-text"><Peek item={peeked} /></span></button>
  {/if}
  {#if open}
    <section class="fl-panel" aria-label={f.title || undefined} onclickcapture={unarmed}>
      <header class="fl-head">
        <i class="fl-mark" aria-hidden="true"></i>
        <h2>{n ? tn("{n} wait on you", n) : t("Nothing waits on you")}</h2>
        <span class="fl-sp"></span>
        {#if n}<span class="fl-meta">{t("{n} of {total}", { n: fmtNumber(f.current + 1), total: fmtNumber(n) })}</span>{/if}
        <button type="button" class="fl-quiet" aria-pressed={f.pinned ? "true" : "false"} onclick={() => f.setPinned(!f.pinned)}>{t("Pin")}</button>
        <span class="fl-menu">
          <button type="button" class="fl-quiet" aria-haspopup="menu" aria-expanded={menu ? "true" : "false"} aria-label={t("Hide")} onclick={() => (menu = !menu)}>⋯</button>
          {#if menu}
            <span class="fl-menu-list" role="menu">
              {#each HIDES as h (h.mode)}<button type="button" role="menuitem" data-hide={h.mode} use:hovered onclick={() => hide(h.mode)}>{h.label()}</button>{/each}
            </span>
          {/if}
        </span>
      </header>
      <div class="fl-body">
        {#if f.read === "offline"}
          <p class="fl-off">{t("The daemon is not answering.")}</p>
        {:else if f.read === "failed"}
          <p class="fl-off">{t("Could not read the queue.")}</p>
        {:else}
          <Agents turns={f.turns} failed={f.turnsFailed} />
          <Queue {f} />
        {/if}
      </div>
      {#if f.failed || (notice.err && notice.text)}<p class="fl-err" role="alert">{f.failed || notice.text}</p>{/if}
    </section>
  {/if}
</div>
