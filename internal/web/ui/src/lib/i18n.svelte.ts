// The cockpit's texts in the daemon's interface language: t and tn read the strings /api/strings serves, by
// their English text, and fmtDate and fmtNumber format in its locale. Until the strings arrive, and when they
// never do, every text reads as its English key. The strings are reactive state, so a strings or locale change
// while the page is open rewords every label that read them.

export type Forms = Record<string, string>;
export type Strings = Record<string, string | Forms>;
export type Params = Record<string, unknown>;

export const I18N = $state({ locale: "en", strings: {} as Strings, stamp: "", latest: "", fetching: "" });

// STRINGS_BUDGET_MS is how long the boot waits for the strings before it renders in English.
export const STRINGS_BUDGET_MS = 1500;

export function interpolate(text: string, params?: Params): string {
  if (!params) return text;
  return text.replace(/\{([A-Za-z0-9_]+)\}/g, (whole, name: string) => (Object.hasOwn(params, name) ? String(params[name]) : whole));
}

export function t(key: string, params?: Params): string {
  const v = I18N.strings[key];
  const text = typeof v === "string" ? v : (v && v.other) || key;
  return interpolate(text, params);
}

export function pluralForm(count: number): string {
  try {
    return new Intl.PluralRules(I18N.locale).select(count);
  } catch {
    return new Intl.PluralRules("en").select(count);
  }
}

export function tn(key: string, count: number, params?: Params): string {
  const v = I18N.strings[key];
  const text = typeof v === "string" ? v : (v && (v[pluralForm(count)] || v.other)) || key;
  return interpolate(text, { count, n: fmtNumber(count), ...params });
}

const DATE_STYLES: Record<string, Intl.DateTimeFormatOptions> = {
  date: { day: "2-digit", month: "2-digit" },
  time: { hour: "2-digit", minute: "2-digit" },
  datetime: { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" },
  long: { weekday: "short", day: "numeric", month: "short", year: "numeric" },
};

export function fmtDate(iso: string | number | Date, style?: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(I18N.locale, DATE_STYLES[style || ""] || DATE_STYLES.datetime).format(d);
}

export function fmtNumber(n: number): string {
  return new Intl.NumberFormat(I18N.locale).format(n);
}

// fmtLanguage is the name of the language a BCP 47 code names, in the interface language; the code when Intl
// names none.
export function fmtLanguage(code: string): string {
  if (!code) return "";
  try {
    return new Intl.DisplayNames([I18N.locale], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
}

export interface StringsBody {
  locale?: unknown;
  strings?: Strings;
  stamp?: string;
}

export function applyStrings(body: StringsBody | null | undefined): boolean {
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

export type GetJSON = (url: string) => Promise<unknown>;

// loadStrings fills I18N from /api/strings through getJSON. It resolves once they arrive, fail, or budgetMs
// passes, whichever comes first; strings that arrive after the budget still apply.
export function loadStrings(getJSON: GetJSON, budgetMs = STRINGS_BUDGET_MS): Promise<void> {
  return new Promise((resolve) => {
    let waiting = true;
    const timer = setTimeout(() => {
      waiting = false;
      resolve();
    }, budgetMs);
    Promise.resolve()
      .then(() => getJSON("/api/strings"))
      .then((body) => {
        applyStrings(body as StringsBody);
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
export function followStrings(stamp: string | undefined, getJSON: GetJSON): Promise<void> {
  if (!stamp) return Promise.resolve();
  I18N.latest = stamp;
  if (stamp === I18N.stamp || stamp === I18N.fetching) return Promise.resolve();
  I18N.fetching = stamp;
  return Promise.resolve()
    .then(() => getJSON("/api/strings"))
    .then((raw) => {
      const body = raw as StringsBody;
      if (body && body.stamp && body.stamp !== I18N.latest) return;
      applyStrings(body);
    })
    .catch(() => {})
    .finally(() => {
      if (I18N.fetching === stamp) I18N.fetching = "";
    });
}
