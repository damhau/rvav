// Small validators for typed search params (filters live in the URL).
export const optString = (v: unknown, max = 200): string | undefined =>
  typeof v === "string" && v.trim() !== "" ? v.slice(0, max) : typeof v === "number" ? String(v) : undefined;

/**
 * A day as a date box keeps it, 2026-11-23; anything else is dropped, such as
 * 2026-02-31, which Date.parse takes as 3 March, or year 0000.
 */
export const optDay = (v: unknown): string | undefined => {
  if (typeof v !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(v) || v < "0001") return undefined;
  const day = new Date(`${v}T00:00:00Z`);
  return !Number.isNaN(day.getTime()) && day.toISOString().slice(0, 10) === v ? v : undefined;
};

export const optBool = (v: unknown): boolean | undefined => (v === true || v === "true" ? true : undefined);

export function optEnum<T extends string>(v: unknown, allowed: readonly T[]): T | undefined {
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : undefined;
}
