<script lang="ts">
  import { onDestroy } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { plugins, state as store } from "../../state/store.svelte";
  import { cockpitHost, publishPlugins, showPluginView, unmountPluginView, type Host } from "./plugins.svelte";

  // The plugin view the route names: its module mounted once into this section, which the plugin then owns, and
  // the rows published to what it subscribed each time they change. Leaving the view, or its plugin going,
  // unmounts it.
  let { view, host = cockpitHost }: { view: string; host?: Host } = $props();

  let box: HTMLElement;
  const on = $derived(plugins.views.has(view));

  $effect(() => {
    if (on) void showPluginView(view, box, host);
    else unmountPluginView();
  });
  // A sync reconciles the rows in place, so what changed is read deep, and the plugin gets a plain copy.
  $effect(() => {
    if (on && store.loaded) publishPlugins($state.snapshot(store.rows));
  });
  onDestroy(unmountPluginView);
</script>

<section id="plugin-view" class="plugin-view" aria-label={t("Plugin view")} hidden={!on} bind:this={box}></section>
