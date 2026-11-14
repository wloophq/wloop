import { readFileSync } from "node:fs";
import type { J } from "../lib/util";

// testdata reads a file of internal/web/testdata, or of another package's testdata by a path from internal/.
export function testdata(path: string): J {
  return JSON.parse(readFileSync(new URL("../../../../" + path, import.meta.url), "utf8"));
}

let cached: J = null;

// goFixtures is internal/web/testdata/cockpit.fixtures.json, which TestTheCockpitFixturesAreWhatGoServes keeps
// to what the Go side serves and answers. Each call gets its own copy.
export function goFixtures(): J {
  cached = cached || testdata("web/testdata/cockpit.fixtures.json");
  return structuredClone(cached);
}

// servedCatalog is GET /api/catalog as the daemon serves the GitHub provider's lists.
export function servedCatalog(): J {
  return testdata("readmodel/prrow/testdata/catalog.json");
}

// viewsCatalog is views_test.go's small views catalog.
export function viewsCatalog(): J {
  const ids: [string, string][] = [["focus", "Focus"], ["inbox", "Inbox"], ["outbox", "Outbox"], ["fits-now", "Fits now"], ["waiting", "Waiting"],
    ["projects", "Projects"], ["someday", "Someday"], ["logbook", "Logbook"]];
  return {
    views: ids.map(([id, label]) => ({ id, label, rail: true })),
    areas: ["OSS", "Hiring"],
    contexts: ["@decide", "@reply"],
    verbs: [{ verb: "approve", handler: "approve", label: "Approve" }, { verb: "edit", handler: "edit", label: "Edit" }, { verb: "dismiss", handler: "drop", label: "Discard" },
      { verb: "retry", handler: "retry", label: "Retry" }],
  };
}

// task is a task as views_test.go's task builds it.
export function task(id: string, state: string, extra: J = {}): J {
  return { id, state, title: "Task " + id, units: [], verbs: [], created: "2026-10-01T09:00:00Z", updated: "2026-10-01T09:00:00Z", ...extra };
}
