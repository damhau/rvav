import { Ban, CircleCheck, CircleDashed, CircleSlash, Clock3 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { Approval, ApprovalStatus, ApprovalVote } from "@/lib/api/types";
import { toText } from "@/lib/utils";
import { Time } from "./misc";

type Tone = "ok" | "warn" | "bad" | "run" | "neutral";

const meta: Record<ApprovalStatus, { label: string; tone: Tone; icon: React.ElementType }> = {
  waiting: { label: "Not open yet", tone: "neutral", icon: CircleDashed },
  pending: { label: "Waiting", tone: "warn", icon: Clock3 },
  approved: { label: "Approved", tone: "ok", icon: CircleCheck },
  rejected: { label: "Rejected", tone: "neutral", icon: Ban },
  cancelled: { label: "Cancelled", tone: "neutral", icon: CircleSlash },
};

export function ApprovalStatusBadge({ status }: { status: ApprovalStatus }) {
  const s = meta[status] ?? { label: status, tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon aria-hidden />
      {s.label}
    </Badge>
  );
}

/** What happens when a pending level gets no answer in time. */
export function dueText(a: Pick<Approval, "on_timeout" | "escalate_to" | "escalated">): string {
  switch (a.on_timeout) {
    case "approve":
      return "then it is approved automatically";
    case "reject":
      return "then it is rejected automatically";
    case "escalate":
      return a.escalated
        ? "then it is rejected automatically"
        : `then it escalates to ${(a.escalate_to ?? []).map((u) => u.name).join(", ") || "the escalation approvers"}`;
    default:
      return "";
  }
}

function Vote({ v }: { v: ApprovalVote }) {
  return (
    <li className="flex flex-col gap-0.5">
      <span>
        <span className="font-medium">{v.user.name}</span>{" "}
        <span className={v.decision === "approve" ? "text-ok" : "text-bad"}>
          {v.decision === "approve" ? "approved" : "rejected"}
        </span>{" "}
        <Time iso={v.decided_at} className="text-faint" />
      </span>
      {v.comment && <span className="text-muted">“{v.comment}”</span>}
      {v.changes.map((c) => (
        <span key={c.field} className="text-muted">
          Changed {c.label.toLowerCase()} from <span className="font-mono text-[12px]">{toText(c.from)}</span> to{" "}
          <span className="font-mono text-[12px]">{toText(c.to)}</span>
        </span>
      ))}
    </li>
  );
}

/** The levels of a request's approval, with who decided what and why. */
export function ApprovalHistory({ levels }: { levels: Approval[] }) {
  return (
    <ol className="flex flex-col gap-3.5" aria-label="Approval levels">
      {levels.map((a) => (
        <li key={a.id} className="flex flex-col gap-1.5 text-[13px]">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-medium">
              {a.level.name}
              {a.level.count > 1 && (
                <span className="font-normal text-faint">
                  {" "}
                  · level {a.level.index} of {a.level.count}
                </span>
              )}
            </span>
            <ApprovalStatusBadge status={a.status} />
          </div>
          <span className="text-xs text-muted">{a.reasons.join(" · ")}</span>
          {a.outcome && <span className="text-xs">{a.outcome}</span>}
          {a.status === "pending" && (
            <span className="text-xs text-muted">
              {a.approvers.length > 0 ? `Waiting for ${a.approvers.map((u) => u.name).join(", ")}` : "Waiting"}
              {a.due_at && (
                <>
                  {" · due "}
                  <Time iso={a.due_at} />
                  {dueText(a) && `, ${dueText(a)}`}
                </>
              )}
            </span>
          )}
          {a.votes.length > 0 && (
            <ul className="flex flex-col gap-1.5 text-xs">
              {a.votes.map((v) => (
                <Vote key={v.user.id} v={v} />
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}
