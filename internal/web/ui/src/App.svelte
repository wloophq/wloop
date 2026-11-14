<script lang="ts">
  import { isCoreView } from "./lib/catalog";
  import { I18N, t } from "./lib/i18n.svelte";
  import { readLens } from "./lib/view";
  import type { Lens, Pipeline } from "./lib/lens";
  import { DASH, EXPLORE, HOME, PLAN, QUEUE } from "./lib/routes";
  import { readHash } from "./state/route.svelte";
  import Dash from "./components/chrome/Dash.svelte";
  import Explore from "./components/chrome/Explore.svelte";
  import LensBar from "./components/chrome/LensBar.svelte";
  import Notice from "./components/chrome/Notice.svelte";
  import Pause from "./components/chrome/Pause.svelte";
  import PlanPage from "./components/chrome/PlanPage.svelte";
  import PluginView from "./components/chrome/PluginView.svelte";
  import Rail from "./components/chrome/Rail.svelte";
  import Search from "./components/chrome/Search.svelte";
  import Sync from "./components/chrome/Sync.svelte";
  import UpdateBanner from "./components/chrome/UpdateBanner.svelte";
  import Warnings from "./components/chrome/Warnings.svelte";
  import DetailResize from "./components/detail/DetailResize.svelte";
  import UnitDetail from "./components/detail/UnitDetail.svelte";
  import { detailOf } from "./components/detail/unit";
  import { openOmnibox } from "./components/keys/install";
  import Keys from "./components/keys/Keys.svelte";
  import Overlays from "./components/overlay/Overlays.svelte";
  import UnitList from "./components/rows/UnitList.svelte";
  import Home from "./components/screens/Home.svelte";
  import Queue from "./components/screens/Queue.svelte";
  import TaskViewPage from "./components/screens/TaskViewPage.svelte";
  import TaskDetail from "./components/task/TaskDetail.svelte";
  import TitleBar from "./components/window/TitleBar.svelte";

  // The cockpit's page: the parts index.html held, each drawn from the store and the route.
  let { inset = false }: { inset?: boolean } = $props();

  const route = $derived(readHash());
  const view = $derived(route.view);
  const task = $derived(!!route.task);
  const unit = $derived(task ? null : detailOf());
  const detail = $derived(task || !!unit);

  $effect(() => {
    document.documentElement.lang = I18N.locale;
  });
  $effect(() => {
    document.body.classList.toggle("has-detail", detail);
  });
</script>

{#snippet unitTools(lens: Lens, res: Pipeline | null)}
  <LensBar {lens} {res} hidden={isCoreView(lens.view)} />
{/snippet}

{#snippet taskTools(lens: Lens, res: Pipeline | null)}
  <LensBar id="task-tools" label={t("Filter, group and sort the tasks")} {lens} {res} hidden={!res} />
{/snippet}

<header id="top">
  {#if inset}<TitleBar part="start" omnibox={openOmnibox} />{/if}
  <Search />
  <Sync />
  <Notice />
  {#if inset}<TitleBar part="end" omnibox={openOmnibox} />{/if}
  <Pause />
</header>
<Warnings />
<UpdateBanner />
<main id="app">
  <Rail omnibox={openOmnibox} />
  <section id="list" aria-label={t("Pull requests")}>
    <PluginView {view} />
    <Explore shown={view === EXPLORE} />
    <PlanPage shown={view === PLAN} />
    <Dash lens={readLens()} shown={view === DASH} />
    <Home hidden={view !== HOME} />
    <Queue hidden={view !== QUEUE} />
    <TaskViewPage tools={taskTools} />
    <UnitList tools={unitTools} />
  </section>
  <DetailResize />
  <aside id="detail" aria-label={t("Detail")} hidden={!detail}>
    {#if task}<TaskDetail />{:else if unit}<UnitDetail />{/if}
  </aside>
</main>
<Overlays />
<Keys />
