import { beforeEach, expect, test } from "vitest";
import { eventData } from "../../lib/catalog";
import { I18N } from "../../lib/i18n.svelte";
import { state } from "../../state/store.svelte";
import { clone, fixtures, useCatalog } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { exploreStatus } from "./explore";
import { ago } from "./facts";
import { planWhen } from "./plan";

const now = Date.parse("2026-11-07T12:00:00Z");

beforeEach(() => resetStore(now));

test("the chrome's words read in pt-BR", () => {
  useCatalog();
  state.explore.result = null;
  I18N.locale = "pt-BR";
  I18N.strings = clone(fixtures.stringsPtBR);
  const got = [exploreStatus(), planWhen({ decision: "blocked" }), planWhen({ decision: "pending", default: "wait" }), ago(new Date(now - 5 * 60000).toISOString())];
  expect(got).toEqual([
    "A busca só roda quando você aperta Enter ou Buscar; cada uma gasta cerca de 1 ponto da cota por hora do github.",
    "bloqueado", "espera você", "há 5 min",
  ]);
});

test("an event shows its data the way its catalog says", () => {
  useCatalog();
  expect([
    eventData({ kind: "review", data: "APPROVED:carol" }),
    eventData({ kind: "merged", data: "0123456789abcdef" }),
    eventData({ kind: "review_requested", data: "team:acme/web" }),
    eventData({ kind: "commented", data: "a note" }),
    eventData({ kind: "merged" }),
  ]).toEqual(["APPROVED", "0123456", "", "a note", ""]);
});
