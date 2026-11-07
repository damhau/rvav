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

export function ApprovalStatusBadge({ status, label }: { status: ApprovalStatus; label?: string }) {
  const s = meta[status] ?? { label: status, tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon aria-hidden />
      {label ?? s.label}
    </Badge>
  );
}

/** What a step's wait came to, when it goes on: an answer, or its timeout. */
const stepLabel = (a: Approval) =>
  a.step_key && a.status === "approved" ? (a.votes.length > 0 ? "Answered" : "Went on") : undefined;

/** What a step's wait said and was answered, for those who may read it. */
/** An answer as a person reads it: a yes or no question was answered in words. */
export function answerText(value: unknown): string {
  if (typeof value === "boolean") return value ? "Yes" : "No";
  return toText(value);
}

function StepQuestion({ a }: { a: Approval }) {
  const q = a.question;
  if (!q) return null;
  const answer = q.answer ?? {};
  const given = q.form.filter((f) => f.key in answer);
  return (
    <>
      {q.message && <p className="text-xs whitespace-pre-wrap">{q.message}</p>}
      {given.length > 0 && (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs">
          {given.map((f) => (
            <div key={f.key} className="contents">
              <dt className="text-muted">{f.label}</dt>
              <dd className="min-w-0 break-words">{answerText(answer[f.key])}</dd>
            </div>
          ))}
        </dl>
      )}
    </>
  );
}

/** What happens when a pending level gets no answer in time. */
export function dueText(a: Pick<Approval, "on_timeout" | "escalate_to" | "escalated" | "step_key">): string {
  // A step's wait fails its step or goes on with no answer (ADR-0033).
  if (a.step_key) {
    if (a.on_timeout === "approve") return "then it goes on with no answer";
    if (a.on_timeout === "reject" || (a.on_timeout === "escalate" && a.escalated)) return "then the step fails";
  }
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

function Vote({ v, step }: { v: ApprovalVote; step: boolean }) {
  return (
    <li className="flex flex-col gap-0.5">
      <span>
        <span className="font-medium">{v.user.name}</span>{" "}
        <span className={v.decision === "approve" ? "text-ok" : "text-bad"}>
          {v.decision === "reject" ? "rejected" : step ? "answered" : "approved"}
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
export function ApprovalHistory({ levels, label = "Approval levels" }: { levels: Approval[]; label?: string }) {
  return (
    <ol className="flex flex-col gap-3.5" aria-label={label}>
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
            <ApprovalStatusBadge status={a.status} label={stepLabel(a)} />
          </div>
          {a.reasons.length > 0 && <span className="text-xs text-muted">{a.reasons.join(" · ")}</span>}
          <StepQuestion a={a} />
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
                <Vote key={v.user.id} v={v} step={Boolean(a.step_key)} />
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}
