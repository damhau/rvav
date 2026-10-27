// Mock approvals (docs/adr/0011-approvals.md): levels stored next to the
// requests, with the same rules as the API for who may decide.
import type {
  Approval,
  ApprovalEditableField,
  ApprovalPreview,
  ApprovalSummary,
  ApprovalVote,
  RequestForm,
  ServiceRequest,
  UserRef,
} from "@/lib/api/types";
import { toText } from "@/lib/utils";
import { networks, sizes, type SizeKey } from "./inventory";

/** A level as stored: everything but what depends on who is looking. */
export type StoredApproval = Omit<Approval, "can_decide" | "editable_fields" | "overdue">;

interface Ctx {
  me: { user: UserRef };
  users: UserRef[];
  forms: Record<string, RequestForm>;
}

/** Whether the viewer may decide: an approver or delegate, not the requester, no vote yet. */
export function canDecide(ctx: Ctx, a: StoredApproval): boolean {
  const named = a.approvers.some((u) => u.id === ctx.me.user.id) || a.delegates.some((u) => u.id === ctx.me.user.id);
  return (
    a.status === "pending" &&
    named &&
    a.requester.id !== ctx.me.user.id &&
    !a.votes.some((v) => v.user.id === ctx.me.user.id)
  );
}

export function isApproverOf(ctx: Ctx, a: StoredApproval): boolean {
  return a.approvers.some((u) => u.id === ctx.me.user.id) || a.delegates.some((u) => u.id === ctx.me.user.id);
}

const overdue = (a: StoredApproval) => a.status === "pending" && !!a.due_at && Date.parse(a.due_at) < Date.now();

export function summaryOf(ctx: Ctx, a: StoredApproval): ApprovalSummary {
  return {
    id: a.id,
    request: a.request,
    project: a.project,
    environment: a.environment,
    requester: a.requester,
    submitted_at: a.submitted_at,
    level: a.level,
    reasons: a.reasons,
    status: a.status,
    due_at: a.due_at ?? null,
    overdue: overdue(a),
    escalated: a.escalated ?? false,
    can_decide: canDecide(ctx, a),
  };
}

export function editableFields(ctx: Ctx, r: ServiceRequest): ApprovalEditableField[] {
  const form = ctx.forms[r.catalog_item.id];
  if (!form) return [];
  return Object.entries(form.ui_schema.fields)
    .filter(([, f]) => f.approver_editable)
    .map(([field, f]) => ({
      field,
      label: f.label,
      value: r.inputs?.[field],
      ...(f.options ? { options: f.options } : {}),
    }));
}

export function viewOf(ctx: Ctx, a: StoredApproval, r: ServiceRequest | undefined): Approval {
  const decide = canDecide(ctx, a);
  return {
    ...a,
    overdue: overdue(a),
    can_decide: decide,
    editable_fields: decide && r ? editableFields(ctx, r) : [],
  };
}

/** A few lines on what the request creates, as the API derives them from the plan. */
export function factsFor(inputs: Record<string, unknown>, host: string | null) {
  const size = sizes[inputs.size as SizeKey];
  const network = networks.find((n) => n.name === inputs.network);
  const facts: { label: string; value: string }[] = [];
  if (size) facts.push({ label: "Size", value: `${size.vcpu} vCPU · ${size.memoryGb} GB · ${size.diskGb} GB` });
  if (host) facts.push({ label: "Names", value: host });
  if (inputs.cluster)
    facts.push({ label: "Placement", value: `${toText(inputs.cluster)} · ${toText(inputs.datastore)}` });
  if (network) facts.push({ label: "Network", value: `${network.name} · ${network.subnet}` });
  if (Array.isArray(inputs.post_config) && inputs.post_config.length)
    facts.push({ label: "After boot", value: (inputs.post_config as string[]).join(", ") });
  return facts;
}

/** The levels of a new request, from its approval preview (one level per set of approvers). */
export function openLevels(
  ctx: Ctx,
  request: ServiceRequest,
  approval: ApprovalPreview,
  host: string | null,
  nextId: () => string,
): StoredApproval[] {
  const claire = ctx.users.find((u) => u.id === "usr_cdubois");
  const levels: StoredApproval[] = [];
  for (const p of approval.policies) {
    for (const l of p.levels) {
      const key = l.approvers.map((u) => u.id).join(",");
      const same = levels.find((x) => x.approvers.map((u) => u.id).join(",") === key);
      if (same) {
        if (!same.reasons.includes(p.reason)) same.reasons.push(p.reason);
        continue;
      }
      levels.push({
        id: nextId(),
        request: { id: request.id, number: request.number, title: request.title, status: request.status },
        project: request.project,
        environment: request.environment,
        requester: request.requester,
        submitted_at: request.submitted_at,
        level: { index: levels.length + 1, count: 0, name: l.name ?? "Approvers" },
        reasons: [p.reason],
        rules: [],
        status: levels.length === 0 ? "pending" : "waiting",
        due_at: levels.length === 0 ? new Date(Date.now() + 48 * 3_600_000).toISOString() : null,
        escalated: false,
        justification: request.justification,
        facts: factsFor(request.inputs ?? {}, host),
        group: null,
        approvers: l.approvers,
        mode: l.mode ?? "any",
        votes: [],
        delegates: [],
        on_timeout: claire ? "escalate" : null,
        escalate_to: claire ? [claire] : null,
        decided_at: null,
        outcome: null,
      });
    }
  }
  levels.forEach((l) => (l.level.count = levels.length));
  return levels;
}

export function vote(me: UserRef, decision: "approve" | "reject", comment: string, changes: ApprovalVote["changes"]) {
  return {
    user: me,
    decision,
    ...(comment ? { comment } : {}),
    changes,
    decided_at: new Date().toISOString(),
  } satisfies ApprovalVote;
}
