import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// Without the type scale's names, tailwind-merge takes text-caption for a
// colour and drops it next to text-muted.
const twMerge = extendTailwindMerge({
  extend: { theme: { text: ["badge", "caption", "secondary", "body", "section", "title"] } },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Any value as display text: objects as JSON, null and undefined as "". */
export function toText(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean" || typeof v === "bigint") return v.toString();
  return JSON.stringify(v);
}

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

export function relative(iso: string, now = Date.now()) {
  const diff = (new Date(iso).getTime() - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return diff < 0 ? "just now" : "in a moment";
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86_400) return rtf.format(Math.round(diff / 3600), "hour");
  if (abs < 86_400 * 30) return rtf.format(Math.round(diff / 86_400), "day");
  return rtf.format(Math.round(diff / (86_400 * 30)), "month");
}

export function fullDate(iso: string) {
  return new Date(iso).toLocaleString("en-GB", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** A day without its time, as "12 March 2027". */
export function longDay(iso: string) {
  return new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric" });
}

const pad = (n: number) => String(n).padStart(2, "0");

/** A date-time as a datetime-local input shows it: local time, to the minute. "" when there is none. */
export function localInput(iso: string | null | undefined): string {
  const d = iso ? new Date(iso) : undefined;
  if (!d || Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** What a datetime-local input holds, as ISO 8601 with this browser's offset: "2026-11-20T14:30:00+01:00". */
export function withOffset(local: string): string {
  const d = new Date(local);
  if (Number.isNaN(d.getTime())) return local;
  return `${localInput(local)}:${pad(d.getSeconds())}${offsetOf(d)}`;
}

function offsetOf(d: Date): string {
  const off = -d.getTimezoneOffset();
  return `${off < 0 ? "-" : "+"}${pad(Math.floor(Math.abs(off) / 60))}:${pad(Math.abs(off) % 60)}`;
}

/** This browser's time zone, as "Europe/Zurich (UTC+01:00)" on a given day. */
export function zoneLabel(at = new Date()): string {
  return `${Intl.DateTimeFormat().resolvedOptions().timeZone} (UTC${offsetOf(at)})`;
}

export function clock(iso?: string | null) {
  if (!iso) return "";
  return new Date(iso).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function duration(from?: string | null, to?: string | null) {
  if (!from) return "";
  const s = Math.max(0, Math.round(((to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return m < 60 ? `${m}m ${String(s % 60).padStart(2, "0")}s` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** "about 7 min" for typical durations. */
export function roughDuration(seconds?: number | null) {
  if (!seconds) return "";
  if (seconds < 90) return "1 min";
  const m = Math.round(seconds / 60);
  return m < 60 ? `about ${m} min` : `about ${Math.round(m / 60)} h`;
}

export function initials(name: string) {
  return name
    .split(/\s+/)
    .map((p) => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export function daysUntil(iso: string) {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000);
}

export function leaseLabel(expiresAt: string | null | undefined) {
  if (!expiresAt) return "No expiry";
  const d = daysUntil(expiresAt);
  if (d <= 0) return "Expired";
  if (d === 1) return "Tomorrow";
  return `In ${d} days`;
}

export function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

/** Keeps each number on the line of the word after it, such as a unit: "60 GB" never breaks. */
export function withUnits(text: string): string {
  return text.replace(/(\d) (?=\p{L})/gu, "$1\u00a0");
}
