const I18N = { locale: "en", strings: {} };

function interpolate(text, params) {
  if (!params) return text;
  return text.replace(/\{([A-Za-z0-9_]+)\}/g, (whole, name) => (Object.hasOwn(params, name) ? String(params[name]) : whole));
}

function t(key, params) {
  const v = I18N.strings[key];
  const text = typeof v === "string" ? v : (v && v.other) || key;
  return interpolate(text, params);
}

function tn(key, count, params) {
  const v = I18N.strings[key];
  const form = count === 1 ? "one" : "other";
  const text = typeof v === "string" ? v : (v && (v[form] || v.other)) || key;
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
