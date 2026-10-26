import { Ban, CircleCheck, CircleDashed, CircleSlash, CircleX, Clock3, LoaderCircle, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { DeploymentStatus, RequestStatus } from "@/lib/api/types";
import { cn } from "@/lib/utils";

type Tone = "ok" | "warn" | "bad" | "run" | "neutral";

const requestMeta: Record<RequestStatus, { label: string; tone: Tone; icon: React.ElementType; spin?: boolean }> = {
  pending_approval: { label: "Awaiting approval", tone: "warn", icon: Clock3 },
  scheduled: { label: "Scheduled", tone: "neutral", icon: Clock3 },
  queued: { label: "Queued", tone: "run", icon: CircleDashed },
  running: { label: "In progress", tone: "run", icon: LoaderCircle, spin: true },
  succeeded: { label: "Succeeded", tone: "ok", icon: CircleCheck },
  failed: { label: "Failed", tone: "bad", icon: CircleX },
  rejected: { label: "Rejected", tone: "neutral", icon: Ban },
  cancelling: { label: "Cancelling", tone: "warn", icon: LoaderCircle, spin: true },
  cancelled: { label: "Cancelled", tone: "neutral", icon: CircleSlash },
};

export const requestStatusLabel = (s: RequestStatus) => requestMeta[s]?.label ?? s;

export function RequestStatusBadge({ status }: { status: RequestStatus }) {
  const s = requestMeta[status] ?? { label: status, tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn(s.spin && "animate-spin")} aria-hidden />
      {s.label}
    </Badge>
  );
}

const deploymentMeta: Record<DeploymentStatus, { label: string; tone: Tone; icon: React.ElementType; spin?: boolean }> =
  {
    ready: { label: "Ready", tone: "ok", icon: CircleCheck },
    provisioning: { label: "Provisioning", tone: "run", icon: LoaderCircle, spin: true },
    updating: { label: "Updating", tone: "run", icon: LoaderCircle, spin: true },
    failed: { label: "Failed", tone: "bad", icon: CircleX },
    deleting: { label: "Deleting", tone: "neutral", icon: Trash2 },
    deleted: { label: "Deleted", tone: "neutral", icon: Trash2 },
  };

export const deploymentStatusLabel = (s: DeploymentStatus) => deploymentMeta[s]?.label ?? s;

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  const s = deploymentMeta[status] ?? { label: status, tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn(s.spin && "animate-spin")} aria-hidden />
      {s.label}
    </Badge>
  );
}

export function EnvBadge({ env, production }: { env: string; production?: boolean }) {
  const prd = production ?? env === "prd";
  return (
    <span
      className={cn(
        "inline-flex h-[18px] items-center rounded-sm border px-1 font-mono text-[10px] font-medium uppercase",
        prd ? "border-bad/50 text-bad" : env === "tst" ? "border-warn/50 text-warn" : "border-line-strong text-muted",
      )}
    >
      {env}
    </span>
  );
}

export function PowerDot({ on, label }: { on: boolean | null; label?: boolean }) {
  const text = on === null ? "Unknown" : on ? "Powered on" : "Powered off";
  return (
    <span className="inline-flex items-center gap-1.5" title={text}>
      <span
        className={cn("size-2 shrink-0 rounded-full", on ? "bg-ok" : "border border-line-strong bg-transparent")}
        aria-hidden
      />
      {label ? <span className="text-xs text-muted">{text}</span> : <span className="sr-only">{text}</span>}
    </span>
  );
}
