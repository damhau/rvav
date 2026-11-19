import type { CustomResource } from "@/lib/api/types";

/** A custom resource's property as the page says it: lists joined, objects as JSON. */
export function propertyText(v: unknown): string {
  if (v === null || v === undefined || v === "") return "None";
  if (Array.isArray(v) && v.every((x) => x === null || typeof x !== "object")) return v.join(", ") || "None";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

/** Its properties, labelled by the titles its type's schema gives them (or their names), titled ones first. */
export function labelledProperties(r: CustomResource): { label: string; text: string }[] {
  const titles = r.property_titles ?? {};
  return Object.keys(r.properties)
    .sort((a, b) => Number(!(a in titles)) - Number(!(b in titles)))
    .map((k) => ({ label: titles[k] ?? k, text: propertyText(r.properties[k]) }));
}
