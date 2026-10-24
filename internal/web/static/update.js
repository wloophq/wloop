"use strict";

// buildAction is what a page that loaded build loaded does once the daemon
// serves build served: nothing while they agree or either is unknown, a
// reload when the page holds nothing a reload would lose and has not
// reloaded for served already, else the banner.
function buildAction(loaded, served, blocked, reloadedFor) {
  if (!loaded || !served || loaded === served) return "";
  return blocked || reloadedFor === served ? "banner" : "reload";
}

// pageBuild is the build the page was served with.
function pageBuild(doc) {
  const m = doc.querySelector('meta[name="build"]');
  return (m && m.getAttribute("content")) || "";
}

// TEXT_INPUTS are the fields a person types into; the filter and the
// explore search are left out, since the hash keeps them.
const TEXT_INPUTS = 'textarea, input:not([type]), input[type="text"], input[type="search"]';
const HASH_INPUTS = new Set(["search", "explore-q"]);

// reloadBlocked says whether a reload would lose something: an open dialog,
// or text typed in a field or kept for a composer, a guidance or an answer
// not shown.
function reloadBlocked(doc) {
  if (doc.querySelector('[role="dialog"], dialog[open], .pal-back')) return true;
  const texts = [...doc.querySelectorAll(TEXT_INPUTS)].filter((el) => !HASH_INPUTS.has(el.id)).map((el) => el.value);
  if (typeof state !== "undefined") texts.push(...Object.values(state.guidance || {}));
  if (typeof detailState !== "undefined") {
    texts.push(detailState.answer, detailState.guidance, detailState.compose.text, detailState.compose.reaction);
    if (detailState.editing.size) return true;
  }
  return texts.some((t) => String(t || "").trim() !== "");
}

const RELOADED_KEY = "reloaded-for";

function reloadedFor() {
  try {
    return sessionStorage.getItem(RELOADED_KEY) || "";
  } catch {
    return "";
  }
}

// checkBuild reloads the page, or offers to, once health names a build
// other than the page's.
function checkBuild(health) {
  const served = (health && health.build) || "";
  const action = buildAction(pageBuild(document), served, reloadBlocked(document), reloadedFor());
  if (action === "reload") {
    try {
      sessionStorage.setItem(RELOADED_KEY, served);
    } catch {
      // Without the session storage a second mismatch reloads again.
    }
    location.reload();
    return;
  }
  const box = document.getElementById("update");
  if (!box) return;
  if (action === "banner") {
    box.hidden = false;
    document.getElementById("update-reload").onclick = () => location.reload();
  } else if (!served || served === pageBuild(document)) {
    box.hidden = true;
  }
}
