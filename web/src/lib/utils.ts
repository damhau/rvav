import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";
import { i18n } from "@/lib/i18n";

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

/** Copies text to the clipboard; rejects where the browser has none (plain HTTP) or refuses. */
export async function copyText(text: string): Promise<void> {
  if (!navigator.clipboard) throw new Error(i18n.t("common:clipboardMissing"));
  await navigator.clipboard.writeText(text);
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

export function initials(name: string) {
  return name
    .split(/\s+/)
    .map((p) => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

/** Keeps each number on the line of the word after it, such as a unit: "60 GB" never breaks. */
export function withUnits(text: string): string {
  return text.replace(/(\d) (?=\p{L})/gu, "$1\u00a0");
}

/**
 * An address a form may show or link to, as the server checks it: http(s),
 * or a path on this site. Anything else (javascript:, data:) isn't used.
 */
export function shownUrl(url: string | undefined): string | undefined {
  const u = url?.trim();
  if (!u) return undefined;
  if (u.startsWith("/")) return u.startsWith("//") || u.includes("\\") ? undefined : u;
  try {
    const parsed = new URL(u);
    return (parsed.protocol === "https:" || parsed.protocol === "http:") && parsed.host ? u : undefined;
  } catch {
    return undefined;
  }
}
