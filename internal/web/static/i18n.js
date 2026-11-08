// The cockpit's texts in the daemon's interface language: t and tn read the strings /api/strings serves, by
// their English text, and fmtDate and fmtNumber format in its locale. Until the strings arrive, and when they
// never do, every text reads as its English key.
const I18N = { locale: "en", strings: {} };

// STRINGS_BUDGET_MS is how long the boot waits for the strings before it renders in English.
const STRINGS_BUDGET_MS = 1500;

function interpolate(text, params) {
  if (!params) return text;
  return text.replace(/\{([A-Za-z0-9_]+)\}/g, (whole, name) => (Object.hasOwn(params, name) ? String(params[name]) : whole));
}

function t(key, params) {
  const v = I18N.strings[key];
  const text = typeof v === "string" ? v : (v && v.other) || key;
  return interpolate(text, params);
}

// tKey is key, a catalog key the client words later through t, for a text the daemon sends as it is.
function tKey(key) {
  return key;
}

function pluralForm(count) {
  try {
    return new Intl.PluralRules(I18N.locale).select(count);
  } catch {
    return new Intl.PluralRules("en").select(count);
  }
}

function tn(key, count, params) {
  const v = I18N.strings[key];
  const text = typeof v === "string" ? v : (v && (v[pluralForm(count)] || v.other)) || key;
  return interpolate(text, { count, n: fmtNumber(count), ...params });
}

const DATE_STYLES = {
  date: { day: "2-digit", month: "2-digit" },
  time: { hour: "2-digit", minute: "2-digit" },
  datetime: { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" },
  long: { weekday: "short", day: "numeric", month: "short", year: "numeric" },
};

function fmtDate(iso, style) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(I18N.locale, DATE_STYLES[style] || DATE_STYLES.datetime).format(d);
}

function fmtNumber(n) {
  return new Intl.NumberFormat(I18N.locale).format(n);
}

// fmtLanguage is the name of the language a BCP 47 code names, in the interface language; the code when Intl
// names none.
function fmtLanguage(code) {
  if (!code) return "";
  try {
    return new Intl.DisplayNames([I18N.locale], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
}

function applyStrings(body) {
  if (!body || typeof body.locale !== "string" || !body.locale) return false;
  try {
    Intl.getCanonicalLocales(body.locale);
  } catch {
    return false;
  }
  I18N.locale = body.locale;
  I18N.strings = body.strings || {};
  I18N.stamp = body.stamp || "";
  return true;
}

// loadStrings fills I18N from /api/strings through getJSON. It resolves once they arrive, fail, or budgetMs
// passes, whichever comes first; strings that arrive after the budget still apply and call i18nChanged.
function loadStrings(getJSON, budgetMs = STRINGS_BUDGET_MS) {
  return new Promise((resolve) => {
    let waiting = true;
    const timer = setTimeout(() => {
      waiting = false;
      resolve();
    }, budgetMs);
    Promise.resolve()
      .then(() => getJSON("/api/strings"))
      .then((body) => {
        if (applyStrings(body) && !waiting && typeof i18nChanged === "function") i18nChanged();
      })
      .catch(() => {})
      .finally(() => {
        if (!waiting) return;
        waiting = false;
        clearTimeout(timer);
        resolve();
      });
  });
}

// followStrings reads the strings again when stamp, the health's stamp of them, differs from the stamp of the
// strings applied: a ui.locale or profile change while the cockpit is open, or a boot read that failed. An answer
// whose stamp is not the latest health's is dropped, and a failed read is retried on the next health.
function followStrings(stamp, getJSON) {
  if (!stamp) return;
  I18N.latest = stamp;
  if (stamp === I18N.stamp || stamp === I18N.fetching) return;
  I18N.fetching = stamp;
  Promise.resolve()
    .then(() => getJSON("/api/strings"))
    .then((body) => {
      if (body && body.stamp && body.stamp !== I18N.latest) return;
      if (applyStrings(body) && typeof i18nChanged === "function") i18nChanged();
    })
    .catch(() => {})
    .finally(() => {
      if (I18N.fetching === stamp) I18N.fetching = "";
    });
}

const STATIC_ATTRS = ["title", "placeholder", "aria-label"];

// translateStatic words the page's own markup: an element's text by its data-i18n key, and its title,
// placeholder and aria-label by their data-i18n-* keys.
function translateStatic(root) {
  for (const el of root.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  for (const attr of STATIC_ATTRS) {
    for (const el of root.querySelectorAll(`[data-i18n-${attr}]`)) el.setAttribute(attr, t(el.getAttribute(`data-i18n-${attr}`)));
  }
}
