import { t } from "../../lib/i18n.svelte";
import { moveFocus } from "../../lib/view";
import { postJSON } from "../../state/api";
import { setNotice } from "../../state/actions.svelte";
import { loadReviewStep, reviewFirstOpen, reviewSteps } from "../../state/loaders.svelte";
import { reviewState } from "../../state/store.svelte";

// STEP_MOVES are how far the review's step keys move.
const STEP_MOVES: Record<string, number> = { next: 1, prev: -1 };

export const reviewTitle = (): string => t("Review");

// reviewKey runs a Review key: done marks the step shown done and moves to the next open one; next and prev move
// between the steps. False when the key is none of them or the review shows no step.
export async function reviewKey(action: string): Promise<boolean> {
  const steps = reviewSteps();
  const at = steps.findIndex((s) => s.id === reviewState.step);
  if (!steps.length || at < 0) return false;
  if (STEP_MOVES[action]) {
    const next = steps[Math.max(0, Math.min(steps.length - 1, at + STEP_MOVES[action]))];
    if (next.id === reviewState.step) return true;
    reviewState.step = next.id;
  } else if (action === "done") {
    if (steps[at].done) {
      setNotice(t("{step} is already done", { step: steps[at].label }));
      return true;
    }
    try {
      reviewState.review = await postJSON(`/api/review/steps/${encodeURIComponent(reviewState.step)}/done`, {});
    } catch (err) {
      setNotice(t("marking {step} done failed: {error}", { step: steps[at].label, error: (err as Error).message }), true);
      return true;
    }
    reviewState.step = reviewState.review.done ? reviewState.step : reviewFirstOpen(reviewSteps(), at);
    setNotice(reviewState.review.done ? t("Review done.") : t("{step}: done", { step: steps[at].label }));
  } else {
    return false;
  }
  await loadReviewStep();
  await moveFocus("review:step:" + reviewState.step);
  return true;
}

// pickStep shows step id and its tasks.
export async function pickStep(id: string): Promise<void> {
  reviewState.step = id;
  await loadReviewStep();
}
