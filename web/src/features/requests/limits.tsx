import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Meter } from "@/components/common/meter";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { q } from "@/lib/query";

type S = components["schemas"];
type Kind = S["DeploymentLimitKind"];

/** The deployment limits in the order the server checks them (ADR-0047). */
export const limitKinds: Kind[] = [
  "deployments_per_user",
  "deployments_per_project",
  "vcpu_per_deployment",
  "memory_gb_per_deployment",
];

/** A deployment limit's name, in the language shown, and unit. */
export const limitLabel: Record<Kind, [string, string]> = {
  get deployments_per_user(): [string, string] {
    return [i18n.t("requests:limit.kind.deployments_per_user"), ""];
  },
  get deployments_per_project(): [string, string] {
    return [i18n.t("requests:limit.kind.deployments_per_project"), ""];
  },
  get vcpu_per_deployment(): [string, string] {
    return [i18n.t("requests:limit.kind.vcpu_per_deployment"), ""];
  },
  get memory_gb_per_deployment(): [string, string] {
    return [i18n.t("requests:limit.kind.memory_gb_per_deployment"), "GB"];
  },
};

export function useEnvName() {
  const envs = useQuery(q.environments());
  return (key: string) => envs.data?.items.find((e) => e.key === key)?.name ?? key;
}

/**
 * A summary's deployment limits: what the owner or the project would have,
 * or the largest deployment's size, against each limit that applies.
 */
export function LimitMeters({ title, lines }: { title: string; lines: S["DeploymentLimitLine"][] }) {
  const { t } = useTranslation("requests");
  const envName = useEnvName();
  if (lines.length === 0) return null;
  return (
    <div className="flex flex-col gap-2.5 border-t border-line px-4 py-3">
      <span className="label">{title}</span>
      {lines.map((l) => (
        <Meter
          key={l.kind}
          label={`${limitLabel[l.kind][0]}${
            l.environment ? ` ${t("limit.inEnvironment", { environment: envName(l.environment) })}` : ""
          }${l.exceeds && l.enforcement === "soft" ? ` · ${t("limit.overSoft")}` : ""}`}
          unit={limitLabel[l.kind][1]}
          used={l.value}
          total={l.limit}
        />
      ))}
    </div>
  );
}
