import type { components } from "@/lib/api/schema";

type QuotaImpact = components["schemas"]["QuotaImpact"];
type QuotaLevel = components["schemas"]["QuotaLevel"];

/** A quota resource's name and unit. */
export const quotaLabel: Record<components["schemas"]["QuotaResource"], [string, string]> = {
  vcpu: ["vCPU", ""],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs", ""],
};

const left = (l: QuotaImpact) => (l.limit == null ? Infinity : l.limit - l.used - Math.max(l.adding, 0));
const refuses = (l: QuotaImpact) => l.exceeds && l.enforcement !== "soft";

/**
 * Every quota level must hold (ADR-0047): of each resource's lines, the one
 * that refuses, else the one with the least left.
 */
export function tightest(lines: QuotaImpact[]): QuotaImpact[] {
  const out = new Map<string, QuotaImpact>();
  for (const l of lines) {
    const had = out.get(l.resource);
    if (!had || Number(refuses(l)) - Number(refuses(had)) > 0 || (refuses(l) === refuses(had) && left(l) < left(had)))
      out.set(l.resource, l);
  }
  return [...out.values()];
}

/** Where a quota line counts, after the resource's name: "" for the project's own. */
export function quotaWhere(l: { level: QuotaLevel; environment?: string; scope?: string }): string {
  switch (l.level) {
    case "environment":
      return ` in ${l.environment ?? l.scope ?? ""}`;
    case "project_user":
      return " · per member";
    case "global":
      return " · all of rvav";
    case "global_user":
      return " · per user, all of rvav";
    default:
      return "";
  }
}
