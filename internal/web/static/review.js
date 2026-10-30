"use strict";

// reviewState is the review on screen: the daemon's Review, the step shown
// and its tasks, which also stand in tasksState so the task keys and the
// detail act on them.
const reviewState = { review: null, step: "", error: "" };

// STEP_MOVES are how far the review's step keys move.
const STEP_MOVES = { next: 1, prev: -1 };

const reviewEnv = {
  getJSON: (url) => getJSON(url),
  post: (path, body) => taskPost(path, body),
  notice: (text, err) => setNotice(text, err),
  render: () => render(),
  focusStep: (id) => {
    const el = typeof document !== "undefined" && document.querySelector(`[data-focus="review:step:${id}"]`);
    if (el) el.focus();
  },
};

// reviewSteps are the views the catalog's review walks, in its order, each
// with the label, count and done mark of the Review's step that shows it.
function reviewSteps() {
  const served = (reviewState.review && reviewState.review.steps) || [];
  const views = (tasksState.catalog && tasksState.catalog.review) || [];
  if (!views.length) return served;
  return views.map((v) => {
    const s = served.find((x) => x.view === v || x.id === v) || {};
    return { id: s.id || v, view: v, label: s.label || viewLabel(v), count: s.count !== undefined ? s.count : ((tasksState.counters || {}).views || {})[v] || 0, done: !!s.done };
  });
}

// reviewFirstOpen is the first step not done after index from (wrapping),
// the last step when every one is done.
function reviewFirstOpen(steps, from = -1) {
  for (let i = 1; i <= steps.length; i++) {
    const s = steps[(from + i) % steps.length];
    if (!s.done) return s.id;
  }
  return steps.length ? steps[steps.length - 1].id : "";
}

function reviewCurrent() {
  return reviewSteps().find((s) => s.id === reviewState.step) || null;
}

function reviewTasksURL(step) {
  return tasksURL(step.view, "", [], "");
}

// loadReview reads the Review and the tasks of the step shown, the first
// open step when none is.
async function loadReview() {
  try {
    reviewState.review = await reviewEnv.getJSON("/api/review");
    reviewState.error = "";
  } catch (err) {
    reviewState.error = `Could not load the review: ${err.message}`;
    return;
  }
  if (!reviewCurrent()) reviewState.step = reviewFirstOpen(reviewSteps());
  await loadReviewStep();
}

async function loadReviewStep() {
  const step = reviewCurrent();
  if (!step) return;
  try {
    const page = await reviewEnv.getJSON(reviewTasksURL(step));
    tasksState.tasks = page.tasks || [];
    tasksState.key = "review|" + step.id;
    tasksState.page = null;
    if (page.counters) tasksState.counters = page.counters;
    tasksState.error = "";
  } catch (err) {
    tasksState.error = `Could not load the ${step.label} step: ${err.message}`;
  }
}

// reviewKey runs a Review key: done marks the step shown done and moves to
// the next open one; next and prev move between the steps.
async function reviewKey(action) {
  const steps = reviewSteps();
  const at = steps.findIndex((s) => s.id === reviewState.step);
  if (!steps.length || at < 0) return false;
  if (STEP_MOVES[action]) {
    const next = steps[Math.max(0, Math.min(steps.length - 1, at + STEP_MOVES[action]))];
    if (next.id === reviewState.step) return true;
    reviewState.step = next.id;
  } else if (action === "done") {
    if (steps[at].done) {
      reviewEnv.notice(`${steps[at].label} is already done`);
      return true;
    }
    try {
      reviewState.review = await reviewEnv.post(`/api/review/steps/${encodeURIComponent(reviewState.step)}/done`, {});
    } catch (err) {
      reviewEnv.notice(`marking ${steps[at].label} done failed: ${err.message}`, true);
      return true;
    }
    reviewState.step = reviewState.review.done ? reviewState.step : reviewFirstOpen(reviewSteps(), at);
    reviewEnv.notice(reviewState.review.done ? "Review done." : `${steps[at].label}: done`);
  } else {
    return false;
  }
  await loadReviewStep();
  reviewEnv.render();
  reviewEnv.focusStep(reviewState.step);
  return true;
}

function reviewStepItem(s) {
  const on = s.id === reviewState.step;
  return h("li", { class: "review-step" + (on ? " sel" : "") + (s.done ? " done" : "") },
    h("button", { type: "button", class: "review-pick", "aria-current": on ? "step" : "false", "data-focus": "review:pick:" + s.id,
      onclick: async () => {
        reviewState.step = s.id;
        await loadReviewStep();
        reviewEnv.render();
      } },
    h("span", { class: "review-mark", "aria-hidden": "true" }, s.done ? "✓" : "○"),
    h("span", { class: "review-label" }, s.label),
    h("span", { class: "count" }, String(s.count))));
}

// reviewPanel is the review: its steps with their counts, then the step
// shown with its tasks.
function reviewPanel(open) {
  const parts = [h("div", { class: "tools tasks-tools" }, h("h2", { class: "tasks-head" }, REVIEW_TITLE))];
  if (reviewState.error) parts.push(h("p", { class: "plan-error" }, reviewState.error));
  const steps = reviewSteps();
  if (!reviewState.review) {
    parts.push(h("p", { class: "empty" }, "Loading."));
    return parts;
  }
  parts.push(h("ol", { class: "review-steps", "aria-label": "Review steps" }, ...steps.map(reviewStepItem)));
  if (reviewState.review.done) parts.push(h("p", { class: "empty review-done" }, "Review done. A new one starts the next time you open it."));
  const step = reviewCurrent();
  if (!step) return parts;
  parts.push(h("div", { class: "review-head" },
    h("h3", { class: "plan-head", tabindex: 0, "data-focus": "review:step:" + step.id, title: "Enter marks the step done; ] and [ move between steps" },
      `${step.label} (${step.count})`, step.done ? " ✓" : ""),
    step.done ? null : h("button", { type: "button", class: "act", "data-focus": "review:done:" + step.id, onclick: () => reviewKey("done") }, "Mark done")));
  if (tasksState.error) parts.push(h("p", { class: "plan-error" }, tasksState.error));
  if (tasksState.key !== "review|" + step.id) {
    parts.push(h("p", { class: "empty" }, "Loading."));
    return parts;
  }
  if (!tasksState.tasks.length) {
    parts.push(h("p", { class: "empty" }, "Nothing here."));
    return parts;
  }
  parts.push(h("ol", { class: "task-list" }, ...tasksState.tasks.map((e) => listItem(e, open))));
  return parts;
}
