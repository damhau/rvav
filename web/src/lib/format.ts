// Dates, times, numbers and lists in the language shown (ADR-0061). Call
// them while rendering, in a component that calls useTranslation (or under
// one, with no memo between): it renders again when the language changes.
import { currentLocale, i18n } from "@/lib/i18n";

const intlLocales = { en: "en-GB", fr: "fr-FR" } as const;

/** The locale Intl formats with: British English, or French. */
export function intlLocale(): string {
  return intlLocales[currentLocale()];
}

// Intl's formatters are slow to build: one per locale and options.
const cache = new Map<string, unknown>();
function cached<T>(kind: string, options: object, make: (locale: string) => T): T {
  const locale = intlLocale();
  const key = `${kind}|${locale}|${JSON.stringify(options)}`;
  let f = cache.get(key) as T | undefined;
  if (!f) {
    f = make(locale);
    cache.set(key, f);
  }
  return f;
}

/** A number, as "1,234.5" or "1 234,5". */
export function formatNumber(n: number, options: Intl.NumberFormatOptions = {}): string {
  return cached("n", options, (l) => new Intl.NumberFormat(l, options)).format(n);
}

/** A date or time with Intl's options, in this browser's time zone unless `options.timeZone` says otherwise. */
export function formatDate(iso: string | number | Date, options: Intl.DateTimeFormatOptions): string {
  return cached("d", options, (l) => new Intl.DateTimeFormat(l, options)).format(new Date(iso));
}

/** A ratio as a percentage: 0.125 is "12.5%", "12,5 %". */
export function formatPercent(ratio: number) {
  return formatNumber(ratio, { style: "percent", maximumFractionDigits: 1 });
}

/** Items as a sentence's list: "a, b and c", "a, b et c"; or "a, b or c" with `type: "disjunction"`. */
export function formatList(items: string[], type: Intl.ListFormatType = "conjunction"): string {
  return cached("l", { type }, (l) => new Intl.ListFormat(l, { style: "long", type })).format(items);
}

/** "3 minutes ago", "il y a 3 minutes"; "just now" within 45 seconds. */
export function relative(iso: string, now = Date.now()) {
  const diff = (new Date(iso).getTime() - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return diff < 0 ? i18n.t("common:time.justNow") : i18n.t("common:time.inAMoment");
  const rtf = cached("r", {}, (l) => new Intl.RelativeTimeFormat(l, { numeric: "auto" }));
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86_400) return rtf.format(Math.round(diff / 3600), "hour");
  if (abs < 86_400 * 30) return rtf.format(Math.round(diff / 86_400), "day");
  return rtf.format(Math.round(diff / (86_400 * 30)), "month");
}

/** "05 Mar 2027, 14:30". */
export function fullDate(iso: string) {
  return formatDate(iso, { day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** A day without its time, as "12 March 2027". */
export function longDay(iso: string) {
  return formatDate(iso, { day: "numeric", month: "long", year: "numeric" });
}

/** "14:30:05". */
export function clock(iso?: string | null) {
  if (!iso) return "";
  return formatDate(iso, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/** How long something took, or has taken so far: "45s", "3m 05s", "2h 3m". */
export function duration(from?: string | null, to?: string | null) {
  if (!from) return "";
  const s = Math.max(0, Math.round(((to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()) / 1000));
  if (s < 60) return i18n.t("common:time.seconds", { s });
  const m = Math.floor(s / 60);
  return m < 60
    ? i18n.t("common:time.minutesSeconds", { m, s: String(s % 60).padStart(2, "0") })
    : i18n.t("common:time.hoursMinutes", { h: Math.floor(m / 60), m: m % 60 });
}

/** "about 7 min" for typical durations. */
export function roughDuration(seconds?: number | null) {
  if (!seconds) return "";
  if (seconds < 90) return i18n.t("common:time.aboutAMinute");
  const m = Math.round(seconds / 60);
  return m < 60
    ? i18n.t("common:time.aboutMinutes", { m })
    : i18n.t("common:time.aboutHours", { h: Math.round(m / 60) });
}

export function daysUntil(iso: string) {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000);
}

/** When a lease ends: "Tomorrow", "In 5 days". */
export function leaseLabel(expiresAt: string | null | undefined) {
  if (!expiresAt) return i18n.t("common:lease.none");
  const d = daysUntil(expiresAt);
  if (d <= 0) return i18n.t("common:lease.expired");
  if (d === 1) return i18n.t("common:lease.tomorrow");
  return i18n.t("common:lease.inDays", { count: d });
}

/** A size with its unit, as "60 GB" or "60 Go"; the number and unit never break apart in French. */
export function formatSize(value: number, unit: "MB" | "GB" | "TB") {
  return i18n.t(`common:units.${unit}`, { value: formatNumber(value, { maximumFractionDigits: 1 }) });
}
