import { Ban, CircleCheck, CircleDashed, CircleSlash, CircleX, Clock3, LoaderCircle, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { i18n } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";
import type { DeploymentStatus, RequestStatus } from "@/lib/api/types";
import { cn } from "@/lib/utils";

type Tone = "ok" | "warn" | "bad" | "run" | "neutral";

const requestMeta: Record<RequestStatus, { tone: Tone; icon: React.ElementType; spin?: boolean }> = {
  pending_approval: { tone: "warn", icon: Clock3 },
  scheduled: { tone: "neutral", icon: Clock3 },
  queued: { tone: "run", icon: CircleDashed },
  running: { tone: "run", icon: LoaderCircle, spin: true },
  succeeded: { tone: "ok", icon: CircleCheck },
  failed: { tone: "bad", icon: CircleX },
  rejected: { tone: "neutral", icon: Ban },
  cancelling: { tone: "warn", icon: LoaderCircle, spin: true },
  cancelled: { tone: "neutral", icon: CircleSlash },
};

export const requestStatusLabel = (s: RequestStatus) => (s in requestMeta ? i18n.t(`common:requestStatus.${s}`) : s);

export function RequestStatusBadge({ status }: { status: RequestStatus }) {
  useTranslation();
  const s = requestMeta[status] ?? { tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn(s.spin && "animate-spin")} aria-hidden />
      {requestStatusLabel(status)}
    </Badge>
  );
}

const deploymentMeta: Record<DeploymentStatus, { tone: Tone; icon: React.ElementType; spin?: boolean }> = {
  ready: { tone: "ok", icon: CircleCheck },
  provisioning: { tone: "run", icon: LoaderCircle, spin: true },
  updating: { tone: "run", icon: LoaderCircle, spin: true },
  failed: { tone: "bad", icon: CircleX },
  deleting: { tone: "neutral", icon: Trash2 },
  deleted: { tone: "neutral", icon: Trash2 },
};

export const deploymentStatusLabel = (s: DeploymentStatus) =>
  s in deploymentMeta ? i18n.t(`common:deploymentStatus.${s}`) : s;

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  useTranslation();
  const s = deploymentMeta[status] ?? { tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn(s.spin && "animate-spin")} aria-hidden />
      {deploymentStatusLabel(status)}
    </Badge>
  );
}

export function EnvBadge({ env, production }: { env: string; production?: boolean }) {
  const prd = production ?? env === "prd";
  return (
    <span
      className={cn(
        "inline-flex h-[18px] items-center rounded-sm border px-1 font-mono text-badge font-medium uppercase",
        prd ? "border-bad/50 text-bad" : env === "tst" ? "border-warn/50 text-warn" : "border-line-strong text-muted",
      )}
    >
      {env}
    </span>
  );
}

export function PowerDot({ on, label }: { on: boolean | null; label?: boolean }) {
  const { t } = useTranslation();
  const text = t(on === null ? "power.unknown" : on ? "power.on" : "power.off");
  return (
    <span className="inline-flex items-center gap-1.5" title={text}>
      <span
        className={cn("size-2 shrink-0 rounded-full", on ? "bg-ok" : "border border-line-strong bg-transparent")}
        aria-hidden
      />
      {label ? <span className="text-secondary text-muted">{text}</span> : <span className="sr-only">{text}</span>}
    </span>
  );
}
