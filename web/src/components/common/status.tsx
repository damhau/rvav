import {
  Ban,
  CircleCheck,
  CircleDashed,
  CircleSlash,
  CircleX,
  Clock3,
  LoaderCircle,
  Trash2,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { DeploymentStatus, Env, PowerState, RequestStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const req: Record<RequestStatus, { label: string; tone: "ok" | "warn" | "bad" | "run" | "neutral"; icon: React.ElementType }> = {
  pending_approval: { label: "Awaiting approval", tone: "warn", icon: Clock3 },
  running: { label: "In progress", tone: "run", icon: LoaderCircle },
  succeeded: { label: "Succeeded", tone: "ok", icon: CircleCheck },
  failed: { label: "Failed", tone: "bad", icon: CircleX },
  rejected: { label: "Rejected", tone: "neutral", icon: Ban },
  cancelled: { label: "Cancelled", tone: "neutral", icon: CircleSlash },
};

export function RequestStatusBadge({ status }: { status: RequestStatus }) {
  const s = req[status];
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn(status === "running" && "animate-spin")} />
      {s.label}
    </Badge>
  );
}

const dep: Record<DeploymentStatus, { label: string; tone: "ok" | "warn" | "bad" | "run" | "neutral"; icon: React.ElementType }> = {
  ready: { label: "Ready", tone: "ok", icon: CircleCheck },
  provisioning: { label: "Provisioning", tone: "run", icon: LoaderCircle },
  updating: { label: "Updating", tone: "run", icon: LoaderCircle },
  failed: { label: "Failed", tone: "bad", icon: CircleX },
  deleting: { label: "Deleting", tone: "neutral", icon: Trash2 },
};

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  const s = dep[status];
  return (
    <Badge tone={s.tone}>
      <s.icon className={cn((status === "provisioning" || status === "updating") && "animate-spin")} />
      {s.label}
    </Badge>
  );
}

export function EnvBadge({ env }: { env: Env }) {
  return (
    <span
      className={cn(
        "inline-flex h-[18px] items-center rounded-sm border px-1 font-mono text-[10px] font-medium uppercase",
        env === "prd" ? "border-bad/40 text-bad" : env === "tst" ? "border-warn/40 text-warn" : "border-line-strong text-muted",
      )}
    >
      {env}
    </span>
  );
}

export function PowerDot({ state, label }: { state: PowerState | "unknown"; label?: boolean }) {
  const text = state === "on" ? "Powered on" : state === "off" ? "Powered off" : state === "suspended" ? "Suspended" : "Unknown";
  return (
    <span className="inline-flex items-center gap-1.5" title={text}>
      <span
        className={cn(
          "size-2 rounded-full",
          state === "on" ? "bg-ok" : state === "suspended" ? "bg-warn" : "border border-line-strong bg-transparent",
        )}
      />
      {label && <span className="text-xs text-muted">{text}</span>}
    </span>
  );
}

export function PendingDot() {
  return <CircleDashed className="size-3.5 text-faint" />;
}
