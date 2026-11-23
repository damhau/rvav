// Small validators for typed search params (filters live in the URL).
export const optString = (v: unknown, max = 200): string | undefined =>
  typeof v === "string" && v.trim() !== "" ? v.slice(0, max) : typeof v === "number" ? String(v) : undefined;

/** A day as a date box keeps it, 2026-11-23; anything else is dropped. */
export const optDay = (v: unknown): string | undefined =>
  typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v) && !Number.isNaN(Date.parse(v)) ? v : undefined;

export const optBool = (v: unknown): boolean | undefined => (v === true || v === "true" ? true : undefined);

export function optEnum<T extends string>(v: unknown, allowed: readonly T[]): T | undefined {
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : undefined;
}
