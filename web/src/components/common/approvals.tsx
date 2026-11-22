import { Ban, CircleCheck, CircleDashed, CircleSlash, Clock3 } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import type { Approval, ApprovalStatus, ApprovalVote } from "@/lib/api/types";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { toText } from "@/lib/utils";
import { Time } from "./misc";

type Tone = "ok" | "warn" | "bad" | "run" | "neutral";

const meta: Record<ApprovalStatus, { tone: Tone; icon: React.ElementType }> = {
  waiting: { tone: "neutral", icon: CircleDashed },
  pending: { tone: "warn", icon: Clock3 },
  approved: { tone: "ok", icon: CircleCheck },
  rejected: { tone: "neutral", icon: Ban },
  cancelled: { tone: "neutral", icon: CircleSlash },
};

export function ApprovalStatusBadge({ status, label }: { status: ApprovalStatus; label?: string }) {
  const { t } = useTranslation();
  const s = meta[status] ?? { tone: "neutral" as const, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon aria-hidden />
      {label ?? (status in meta ? t(`approval.status.${status}`) : status)}
    </Badge>
  );
}

/** Whom a level asks besides the users it names: project roles' holders and the requester's manager. */
function askedOf(a: Approval): string | undefined {
  const roles = a.roles?.length
    ? formatList(
        a.roles.map((r) => r.name),
        "disjunction",
      )
    : undefined;
  if (roles && a.manager) return i18n.t("common:approval.asks.rolesAndManager", { roles });
  if (roles) return i18n.t("common:approval.asks.roles", { roles });
  if (a.manager) return i18n.t("common:approval.asks.manager");
  return undefined;
}

/** What a step's wait came to, when it goes on: an answer, or its timeout. */
const stepLabel = (a: Approval) =>
  a.step_key && a.status === "approved"
    ? i18n.t(a.votes.length > 0 ? "common:approval.answered" : "common:approval.wentOn")
    : undefined;

/** An answer as a person reads it: a yes or no question was answered in words. */
export function answerText(value: unknown): string {
  if (typeof value === "boolean") return i18n.t(value ? "common:approval.yes" : "common:approval.no");
  return toText(value);
}

function StepQuestion({ a }: { a: Approval }) {
  const q = a.question;
  if (!q) return null;
  const answer = q.answer ?? {};
  const given = q.form.filter((f) => f.key in answer);
  return (
    <>
      {q.message && <p className="text-secondary whitespace-pre-wrap">{q.message}</p>}
      {given.length > 0 && (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-secondary">
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
    if (a.on_timeout === "approve") return i18n.t("common:approval.due.stepGoesOn");
    if (a.on_timeout === "reject" || (a.on_timeout === "escalate" && a.escalated))
      return i18n.t("common:approval.due.stepFails");
  }
  switch (a.on_timeout) {
    case "approve":
      return i18n.t("common:approval.due.approved");
    case "reject":
      return i18n.t("common:approval.due.rejected");
    case "escalate":
      if (a.escalated) return i18n.t("common:approval.due.rejected");
      return a.escalate_to?.length
        ? i18n.t("common:approval.due.escalates", { people: a.escalate_to.map((u) => u.name).join(", ") })
        : i18n.t("common:approval.due.escalatesToApprovers");
    default:
      return "";
  }
}

function Vote({ v, step }: { v: ApprovalVote; step: boolean }) {
  const { t } = useTranslation();
  const said = v.decision === "reject" ? "rejected" : step ? "answered" : "approved";
  return (
    <li className="flex flex-col gap-0.5">
      <span>
        <Trans
          t={t}
          i18nKey={`approval.vote.${said}`}
          values={{ name: v.user.name }}
          components={{
            name: <span className="font-medium" />,
            tone: <span className={v.decision === "approve" ? "text-ok" : "text-bad"} />,
            time: <Time iso={v.decided_at} className="text-faint" />,
          }}
        />
      </span>
      {v.comment && <span className="text-muted">{t("approval.vote.comment", { comment: v.comment })}</span>}
      {v.changes.map((c) => (
        <span key={c.field} className="text-muted">
          <Trans
            t={t}
            i18nKey="approval.vote.changed"
            values={{ field: c.label.toLowerCase(), from: toText(c.from), to: toText(c.to) }}
            components={{ value: <span className="font-mono text-secondary" /> }}
          />
        </span>
      ))}
    </li>
  );
}

/** The levels of a request's approval, with who decided what and why. */
export function ApprovalHistory({ levels, label }: { levels: Approval[]; label?: string }) {
  const { t } = useTranslation();
  return (
    <ol className="flex flex-col gap-3.5" aria-label={label ?? t("approval.levels")}>
      {levels.map((a) => (
        <li key={a.id} className="flex flex-col gap-1.5 text-body">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-medium">
              {a.level.name}
              {a.level.count > 1 && (
                <span className="font-normal text-faint">
                  {" · "}
                  {t("approval.levelOf", { index: a.level.index, total: a.level.count })}
                </span>
              )}
            </span>
            <ApprovalStatusBadge status={a.status} label={stepLabel(a)} />
          </div>
          {a.reasons.length > 0 && <span className="text-secondary text-muted">{a.reasons.join(" · ")}</span>}
          {askedOf(a) && <span className="text-secondary text-muted">{askedOf(a)}</span>}
          <StepQuestion a={a} />
          {a.outcome && <span className="text-secondary">{a.outcome}</span>}
          {a.status === "pending" && (
            <span className="text-secondary text-muted">
              {a.approvers.length > 0
                ? t("approval.waitingFor", { people: a.approvers.map((u) => u.name).join(", ") })
                : t("approval.waiting")}
              {a.due_at && (
                <>
                  {" · "}
                  <Trans t={t} i18nKey="approval.dueAt" components={{ time: <Time iso={a.due_at} /> }} />
                  {dueText(a) && `, ${dueText(a)}`}
                </>
              )}
            </span>
          )}
          {a.votes.length > 0 && (
            <ul className="flex flex-col gap-1.5 text-secondary">
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
