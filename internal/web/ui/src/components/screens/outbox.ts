import { t } from "../../lib/i18n.svelte";
import { postJSON } from "../../state/api";
import { setNotice } from "../../state/actions.svelte";
import { scheduleLoad } from "../../state/sync.svelte";
import { outboxState } from "../../state/store.svelte";

// disableWorkflow turns workflow off once its inline confirm is pressed.
export async function disableWorkflow(workflow: string): Promise<void> {
  outboxState.confirming = "";
  try {
    await postJSON("/api/workflows/" + encodeURIComponent(workflow) + "/disable", { source: "cockpit" });
    setNotice(t("Stopped {workflow}", { workflow }));
    scheduleLoad({ tasks: true });
  } catch (err) {
    setNotice(t("{action} failed: {error}", { action: t("Stop drafting these"), error: (err as Error).message }), true);
  }
}
