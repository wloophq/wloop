<script lang="ts">
  import { EXPLORE, RECENT } from "../../lib/catalog";
  import { fmtNumber, t } from "../../lib/i18n.svelte";
  import { fieldById, fillWord, groupField, lensWords, NO_GROUP, parseSort, servedLayout, stripColor, tagKeyField, type Lens, type Pipeline } from "../../lib/lens";
  import { clearLens, setGroup } from "../../lib/view";
  import { writeHash } from "../../state/route.svelte";
  import { catalog, type Field } from "../../state/store.svelte";
  import { ariaKey, servedKey } from "../../state/keycaps.svelte";
  import LensChips from "./LensChips.svelte";
  import { ACTIVITY_FIELDS, copyList, openFilterPalette, sortFields } from "./toolbar";

  // The lens bar of a list: the Group and Sort pickers over the fields its lens offers, a removable chip per
  // filter, "+ Filter", the strip's legend, then the count, the list/board switch and Copy list. Recent shows only
  // its filters' chips; Explore none.
  let {
    lens,
    res = null,
    id = "tools",
    label = undefined,
    hidden = false,
  }: { lens: Lens; res?: Pipeline | null; id?: string; label?: string; hidden?: boolean } = $props();

  const recent = $derived(lens.view === RECENT);
  const off = $derived(hidden || lens.view === EXPLORE || (recent && !lens.quals.length));
  const w = $derived(lensWords());
  const offered = $derived(lens.offered || catalog.fields);
  const fields = $derived(lens.fields || catalog.fields);
  const groups = $derived.by(() => {
    const current = tagKeyField(lens.group, fields);
    const out = offered.filter((f) => f.group && f.menu !== false);
    if (current && !out.some((f) => f.id === current.id)) out.push(current);
    return out;
  });
  const board = $derived(lens.layout === "board");
  const own = $derived(lens.defaultGroup || (board ? lens.boardGroup : ""));
  const ownField = $derived(own ? groupField(own, fields) : null);
  const groupOptions = $derived([
    ["", ownField ? fillWord(lens.defaultGroup ? w.default_group : w.board_group, { label: ownField.label }) : w.no_group],
    ...(ownField ? [[NO_GROUP, w.no_group]] : []),
    ...groups.map((f) => [f.id, f.label]),
  ] as [string, string][]);
  const sort = $derived(parseSort(lens.sort, fields));
  const sortOptions = $derived([["", lens.defaultSort], ...sortFields(lens).map((f) => [f.id, f.label])] as [string, string][]);
  const sortTitle = $derived(sort.field ? fillWord(w.sort_by, { label: (fieldById(sort.field, fields) as Field).label.toLowerCase() }) : "");
  const filterKey = $derived(servedKey("filter:add"));
  const strip = $derived.by(() => {
    const s = lens.stripSpec;
    if (!s || !s.rank || (offered && !offered.some((x) => x.id === s.id))) return null;
    return s;
  });
  const listLayout = $derived(servedLayout(lens.view) === "board" ? "list" : "");
</script>

<div {id} class="tools" role="toolbar" aria-label={label ?? t("Filter, group and sort")} hidden={off}>
  {#if !off && recent}
    <LensChips {lens} applied={ACTIVITY_FIELDS} />
    <button type="button" class="act" data-focus="lens:clear" onclick={clearLens}>{t("Clear")}</button>
  {:else if !off}
    <label class="pill lens-pick"
      ><span class="lens-label">{t("Group")}</span><select data-focus="lens:group" value={lens.group} onchange={(e) => setGroup(e.currentTarget.value)}>
        {#each groupOptions as [v, text] (v)}<option value={v}>{text}</option>{/each}
      </select></label
    >
    <span class="pill sort-pick"
      ><label class="pill lens-pick"
        ><span class="lens-label">{t("Sort")}</span><select data-focus="lens:sort" title={sortTitle || undefined} value={sort.field} onchange={(e) => writeHash({ s: e.currentTarget.value })}>
          {#each sortOptions as [v, text] (v)}<option value={v}>{text}</option>{/each}
        </select></label
      ><button
        type="button"
        class="dir"
        data-focus="lens:dir"
        disabled={!sort.field}
        aria-label={sort.desc ? t("Descending") : t("Ascending")}
        title={sort.field ? (sort.desc ? t("Sorted descending; click to flip") : t("Sorted ascending; click to flip")) : t("Pick a sort to choose its direction")}
        onclick={() => writeHash({ s: sort.field + (sort.desc ? "" : "-desc") })}>{sort.desc ? "↓" : "↑"}</button
      ></span
    >
    <LensChips {lens} />
    <button type="button" class="pill add" data-focus="lens:add-filter" aria-keyshortcuts={filterKey ? ariaKey(filterKey) : undefined} onclick={() => openFilterPalette()}
      >{t("Filter")}{#if filterKey}{" "}<kbd>{filterKey}</kbd>{/if}</button
    >
    {#if strip}
      <span class="legend" title={(lens.words || w).legend_title || undefined}
        ><span class="lens-label">{`${strip.label}:`}</span>{#each strip.rank as v (v)}<span class={["lg", "w-" + stripColor(strip, v)].join(" ")}><span class="sw" aria-hidden="true"></span>{(strip.labels || {})[v] || v}</span>{/each}</span
      >
    {/if}
    <span class="lens-count">{res ? t("{shown} of {total}", { shown: fmtNumber(res.shown), total: fmtNumber(res.total) }) : ""}</span>
    {#if lens.q.trim() || lens.group || sort.field}
      <button type="button" class="act" data-focus="lens:clear" onclick={clearLens}>{t("Clear")}</button>
    {/if}
    <div class="seg" role="group" aria-label={t("Layout")}>
      <button type="button" aria-pressed={(!board) ? "true" : "false"} data-focus={`lens:${listLayout || "list"}`} onclick={() => writeHash({ layout: listLayout })}>{t("List")}</button
      ><button type="button" aria-pressed={(board) ? "true" : "false"} data-focus="lens:board" onclick={() => writeHash({ layout: "board" })}>{t("Board")}</button>
    </div>
    <button type="button" class="pill" data-focus="lens:copy-list" title={t("Copy the items shown here, in this order, as YAML, to paste for an agent")} onclick={() => copyList(lens)}
      >{t("Copy list")}</button
    >
  {/if}
</div>
