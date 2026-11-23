import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";

type QuotaImpact = components["schemas"]["QuotaImpact"];
type QuotaLevel = components["schemas"]["QuotaLevel"];

/** A quota resource's name, in the language shown, and unit. */
export const quotaLabel: Record<components["schemas"]["QuotaResource"], [string, string]> = {
  get vcpu(): [string, string] {
    return [i18n.t("common:quota.vcpu"), ""];
  },
  get memory_gb(): [string, string] {
    return [i18n.t("common:quota.memory_gb"), "GB"];
  },
  get storage_gb(): [string, string] {
    return [i18n.t("common:quota.storage_gb"), "GB"];
  },
  get vm_count(): [string, string] {
    return [i18n.t("common:quota.vm_count"), ""];
  },
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
export function quotaWhere(
  l: { level: QuotaLevel; environment?: string; scope?: string },
  envName: (key: string) => string,
): string {
  switch (l.level) {
    case "environment":
      return ` ${i18n.t("requests:quotaWhere.environment", { environment: envName(l.environment ?? l.scope ?? "") })}`;
    case "project_user":
      return ` · ${i18n.t("requests:quotaWhere.member")}`;
    case "global":
      return ` · ${i18n.t("requests:quotaWhere.global")}`;
    case "global_user":
      return ` · ${i18n.t("requests:quotaWhere.globalUser")}`;
    default:
      return "";
  }
}
