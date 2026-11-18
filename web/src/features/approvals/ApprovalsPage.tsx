import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { SquareCheckBig } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { toast } from "sonner";
import { ApprovalHistory, ApprovalStatusBadge, dueText } from "@/components/common/approvals";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { type Column, DataTable } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { Approval, ApprovalEditableField, ApprovalSummary } from "@/lib/api/types";
import { useNow } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, relative, toText } from "@/lib/utils";

export interface ApprovalsSearch {
  /** The approval open in the side panel. */
  id?: string;
  tab?: "decided";
}

type Decision = "approve" | "reject";

function levelText(a: ApprovalSummary) {
  return a.step_key
    ? `A step asks: ${a.level.name}`
    : `${a.level.name}${a.level.count > 1 ? ` · level ${a.level.index} of ${a.level.count}` : ""}`;
}

function columnsFor(decided: boolean): Column<ApprovalSummary>[] {
  return [
    {
      id: "request",
      header: "Request",
      cell: (a) => (
        <>
          <Link
            to="/approvals"
            search={decided ? { tab: "decided", id: a.id } : { id: a.id }}
            className="font-medium text-fg hover:text-accent"
          >
            {a.request.title}
          </Link>
          <div className="text-caption text-muted">
            <span className="font-mono">{a.request.number}</span> · {levelText(a)}
          </div>
        </>
      ),
    },
    {
      id: "why",
      header: "Why",
      cell: (a) => (
        <span className="text-secondary">{a.step_key ? "A step waits for a person" : a.reasons.join("; ")}</span>
      ),
    },
    {
      id: "requester",
      header: "Requested by",
      sortValue: (a) => a.requester.name,
      cell: (a) => (
        <>
          {a.requester.name}
          <div className="text-caption text-muted">{a.project.name}</div>
        </>
      ),
    },
    { id: "env", header: "Environment", cell: (a) => <EnvBadge env={a.environment} /> },
    {
      id: "submitted",
      header: "Submitted",
      sortValue: (a) => a.submitted_at,
      cell: (a) => (
        <span className="text-secondary text-muted">
          <Time iso={a.submitted_at} />
        </span>
      ),
    },
    decided
      ? { id: "status", header: "Decision", cell: (a) => <ApprovalStatusBadge status={a.status} /> }
      : {
          id: "due",
          header: "Due",
          sortValue: (a) => a.due_at,
          cell: (a) =>
            a.overdue ? (
              <span className="font-medium text-bad">Overdue</span>
            ) : a.due_at ? (
              <span className="tnum text-secondary">{relative(a.due_at)}</span>
            ) : (
              <span className="text-secondary text-muted">No deadline</span>
            ),
        },
  ];
}

/** Records a decision, then refreshes what shows it. */
function useDecide({
  onDecided,
  onCommentError,
}: {
  onDecided?: (a: Approval) => void;
  onCommentError?: (message: string) => void;
}) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; decision: Decision; comment?: string; inputs?: Record<string, unknown> }) =>
      call(
        api.POST("/approvals/{approvalId}/decision", {
          params: { path: { approvalId: v.id } },
          body: { decision: v.decision, comment: v.comment, inputs: v.inputs },
        }),
      ),
    onSuccess: (a, { decision }) => {
      qc.setQueryData(keys.approvals.detail(a.id), a);
      void qc.invalidateQueries({ queryKey: keys.approvals.lists() });
      void qc.invalidateQueries({ queryKey: keys.approvals.forRequest(a.request.id) });
      void qc.invalidateQueries({ queryKey: keys.requests.detail(a.request.number) });
      const what =
        decision === "approve" ? (a.status === "approved" ? "Approved" : "Your approval is recorded") : "Rejected";
      toast.success(`${what}: ${a.request.number}`, {
        description:
          a.status === "pending" ? "The level waits for the other approvers." : "The requester has been told.",
      });
      onDecided?.(a);
    },
    onError: (e) => {
      const commentErr = isApiError(e) ? e.problem.errors?.find((f) => f.field === "/comment") : undefined;
      if (commentErr && onCommentError) {
        onCommentError(commentErr.message);
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : "The decision was not saved", {
        description: isApiError(e) ? (e.problem.errors?.[0]?.message ?? e.problem.detail) : undefined,
      });
    },
  });
}

function EditableInput({
  field,
  value,
  onChange,
}: {
  field: ApprovalEditableField;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const id = useId();
  if (field.options?.length) {
    return (
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1 text-secondary font-medium">{field.label}</legend>
        <div className="grid gap-1.5 sm:grid-cols-2">
          {field.options.map((o) => (
            <label
              key={o.value}
              className={cn(
                "flex cursor-pointer items-start gap-2 rounded-md border border-line px-2.5 py-2 text-body hover:bg-sunken",
                value === o.value && "border-accent bg-accent-soft hover:bg-accent-soft",
              )}
            >
              <input
                type="radio"
                name={id}
                value={o.value}
                checked={value === o.value}
                onChange={() => onChange(o.value)}
                className="mt-0.5 accent-[var(--accent)]"
              />
              <span className="flex flex-col">
                <span>
                  {o.label}
                  {o.value === field.value && <span className="text-faint"> · requested</span>}
                </span>
                {o.meta && <span className="text-secondary text-muted">{o.meta}</span>}
              </span>
            </label>
          ))}
        </div>
      </fieldset>
    );
  }
  return (
    <Field label={field.label} htmlFor={id}>
      <input
        id={id}
        type="number"
        className="h-8 w-32 rounded-md border border-line-strong bg-surface px-2.5 text-body"
        min={field.minimum}
        max={field.maximum}
        value={toText(value)}
        onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
      />
    </Field>
  );
}

/** What the approval asks and why: the same for those who decide and those who read. */
function ApprovalFacts({ a }: { a: Approval }) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-1.5 text-secondary text-muted">
        <span className="font-mono">{a.request.number}</span> · {a.requester.name} · {a.project.name} ·{" "}
        <Time iso={a.submitted_at} /> <EnvBadge env={a.environment} production={a.environment === "prd"} />
      </div>
      {a.step_key ? (
        <div className="rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-body">
          <span className="font-medium">
            {a.can_decide
              ? "A step of this request waits for your answer"
              : "A step of this request waits for a person"}
          </span>
          {a.question?.message && <p className="mt-0.5 whitespace-pre-wrap">{a.question.message}</p>}
        </div>
      ) : (
        <div className="rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-body">
          <span className="font-medium">{a.can_decide ? "Why this needs you" : "Why it needs approval"}: </span>
          {a.reasons.join("; ")}
          {a.rules.length > 0 && (
            <span className="mt-0.5 block font-mono text-caption break-words text-muted">{a.rules.join("  ·  ")}</span>
          )}
        </div>
      )}
      {a.facts.length > 0 && <KeyValues items={a.facts.map((f) => ({ label: f.label, value: f.value }))} />}
      {a.justification && (
        <div className="flex flex-col gap-0.5 text-body">
          <span className="text-secondary font-medium text-muted">Requester’s reason</span>
          <p>{a.justification}</p>
        </div>
      )}
      {a.status === "pending" && a.due_at && (
        <p className="text-secondary text-muted">
          Due <Time iso={a.due_at} />
          {dueText(a) && ` · ${dueText(a)}`}
        </p>
      )}
    </>
  );
}

/**
 * An approval in the side panel: what it asks, and for an approver the
 * inputs they may change, a comment, Approve and Reject.
 */
function ApprovalSheet({
  id,
  focusComment,
  onClose,
  onDecided,
  returnFocus,
}: {
  id: string;
  focusComment: boolean;
  onClose: () => void;
  onDecided: (a: Approval) => void;
  returnFocus: () => HTMLElement | null | undefined;
}) {
  const query = useQuery(q.approval(id));
  const a = query.data;
  const commentId = useId();
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const [edits, setEdits] = useState<Record<string, unknown>>({});
  const [comment, setComment] = useState("");
  const [commentError, setCommentError] = useState<string>();
  const deciding = Boolean(a?.can_decide && !a.step_key);
  const decide = useDecide({
    onDecided,
    onCommentError: (m) => {
      setCommentError(m);
      commentRef.current?.focus();
    },
  });

  useEffect(() => {
    if (focusComment && deciding) commentRef.current?.focus();
  }, [focusComment, deciding]);

  const changed = (a?.editable_fields ?? []).filter((f) => f.field in edits && edits[f.field] !== f.value);
  const changeLabel = changed
    .map((f) => f.options?.find((o) => o.value === edits[f.field])?.label ?? toText(edits[f.field]))
    .join(", ");
  const send = (decision: Decision) => {
    if (decision === "reject" && !comment.trim()) {
      setCommentError("Say why you reject it: the requester sees this comment.");
      commentRef.current?.focus();
      return;
    }
    decide.mutate({
      id,
      decision,
      comment: comment.trim() || undefined,
      inputs:
        decision === "approve" && changed.length
          ? Object.fromEntries(changed.map((f) => [f.field, edits[f.field]]))
          : undefined,
    });
  };

  const openRequest = a && (
    <Button variant="secondary" asChild>
      <Link to="/requests/$requestId" params={{ requestId: a.request.number }}>
        Open the request
      </Link>
    </Button>
  );

  return (
    <Sheet
      open
      onOpenChange={(open) => !open && onClose()}
      title={a?.request.title ?? "Approval"}
      description={a ? levelText(a) : undefined}
      returnFocus={returnFocus}
      footer={
        a &&
        (deciding ? (
          <>
            {openRequest}
            <Button variant="danger" disabled={decide.isPending} onClick={() => send("reject")}>
              Reject
            </Button>
            <Button variant="primary" disabled={decide.isPending} onClick={() => send("approve")}>
              {changed.length ? `Approve with ${changeLabel}` : "Approve"}
            </Button>
          </>
        ) : a.can_decide && a.step_key ? (
          <Button variant="primary" asChild>
            <Link to="/requests/$requestId" params={{ requestId: a.request.number }}>
              Answer on the request's page
            </Link>
          </Button>
        ) : (
          openRequest
        ))
      }
    >
      {query.isPending ? (
        <div className="flex flex-col gap-3" role="status" aria-label="Loading">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-12" />
          <Skeleton className="h-28" />
        </div>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} className="p-0" />
      ) : (
        <>
          <ApprovalFacts a={query.data} />
          {deciding ? (
            <div className="flex flex-col gap-3.5 border-t border-line pt-3.5">
              {query.data.editable_fields.map((f) => (
                <EditableInput
                  key={f.field}
                  field={f}
                  value={f.field in edits ? edits[f.field] : f.value}
                  onChange={(v) => setEdits((old) => ({ ...old, [f.field]: v }))}
                />
              ))}
              <Field
                label="Comment"
                htmlFor={commentId}
                hint="The requester reads it. Required to reject."
                error={commentError}
              >
                <Textarea
                  id={commentId}
                  ref={commentRef}
                  value={comment}
                  onChange={(e) => {
                    setComment(e.target.value);
                    setCommentError(undefined);
                  }}
                />
              </Field>
            </div>
          ) : (
            !query.data.step_key && (
              <div className="border-t border-line pt-3.5">
                <ApprovalHistory levels={[query.data]} />
              </div>
            )
          )}
        </>
      )}
    </Sheet>
  );
}

function RowItems({
  a,
  meId,
  onReview,
  onReject,
  onApprove,
}: {
  a: ApprovalSummary;
  meId: string;
  onReview: () => void;
  onReject: () => void;
  onApprove: () => void;
}) {
  const navigate = useNavigate();
  const why =
    a.requester.id === meId ? "You can't decide your own request." : "You have answered, or it isn't yours to decide.";
  const reason = !a.can_decide && <span className="text-caption leading-snug text-faint">{why}</span>;
  return (
    <div className="min-w-56">
      <MenuItem onSelect={onReview}>Review</MenuItem>
      {a.status === "pending" &&
        (a.step_key ? (
          <MenuItem
            disabled={!a.can_decide}
            onSelect={() => void navigate({ to: "/requests/$requestId", params: { requestId: a.request.number } })}
          >
            <span className="flex flex-col">
              Answer on the request's page
              {reason}
            </span>
          </MenuItem>
        ) : (
          <>
            <MenuItem disabled={!a.can_decide} onSelect={onApprove}>
              <span className="flex flex-col">
                Approve
                {reason}
              </span>
            </MenuItem>
            <MenuItem disabled={!a.can_decide} danger onSelect={onReject}>
              <span className="flex flex-col">
                Reject…
                {reason}
              </span>
            </MenuItem>
          </>
        ))}
      <MenuSeparator />
      <MenuItem onSelect={() => void navigate({ to: "/requests/$requestId", params: { requestId: a.request.number } })}>
        Open the request
      </MenuItem>
    </div>
  );
}

export function ApprovalsPage({ search }: { search: ApprovalsSearch }) {
  useNow(30_000);
  const navigate = useNavigate({ from: "/approvals" });
  const { me } = useCurrentProject();
  const decided = search.tab === "decided";
  const list = useQuery(q.approvals(decided ? ["approved", "rejected"] : ["pending"]));
  const listRef = useRef<HTMLDivElement>(null);
  const [focusComment, setFocusComment] = useState(false);
  // Set once a decision removes its row: the focus goes to the next one.
  const decidedId = useRef<string | null>(null);

  const open = (id?: string, comment = false) => {
    setFocusComment(comment);
    void navigate({ search: (s: ApprovalsSearch) => ({ ...s, id }), replace: !id });
  };
  const nextInList = () => {
    const gone = decidedId.current;
    decidedId.current = null;
    if (!gone) return null;
    const links = [...(listRef.current?.querySelectorAll<HTMLAnchorElement>("tbody a[href]") ?? [])];
    return (
      links.find((l) => new URL(l.href).searchParams.get("id") !== gone) ??
      document.querySelector<HTMLElement>('main [role="tab"][aria-selected="true"]')
    );
  };
  const quick = useDecide({
    onDecided: (a) => {
      if (a.status === "pending") return;
      decidedId.current = a.id;
      nextInList()?.focus();
    },
  });

  const total = list.data?.total;
  return (
    <Page>
      <PageHeader
        title="Approvals"
        description="Requests that wait for your decision. Approving lets them start at once."
        meta={
          total !== undefined && (
            <span className="tnum">
              {decided ? `${total} decided, newest first` : `${total} waiting, oldest first`}
            </span>
          )
        }
      />
      <Tabs
        value={decided ? "decided" : "waiting"}
        onValueChange={(v) => void navigate({ search: v === "decided" ? { tab: "decided" } : {} })}
      >
        <TabsList aria-label="Approvals">
          <TabsTrigger value="waiting">Waiting for you</TabsTrigger>
          <TabsTrigger value="decided">Decided</TabsTrigger>
        </TabsList>
        <TabsContent value={decided ? "decided" : "waiting"} className="pt-4">
          <div ref={listRef}>
            <DataTable
              label={decided ? "Decided approvals" : "Waiting for you"}
              columns={columnsFor(decided)}
              rows={list.data?.items}
              rowKey={(a) => a.id}
              rowLinks
              rowMenu={(a) => (
                <RowItems
                  a={a}
                  meId={me.user.id}
                  onReview={() => open(a.id)}
                  onReject={() => open(a.id, true)}
                  onApprove={() => quick.mutate({ id: a.id, decision: "approve" })}
                />
              )}
              rowMenuLabel={(a) => `Actions for ${a.request.number}`}
              minWidth={900}
              loading={list.isPending}
              error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
              empty={
                <EmptyState
                  icon={SquareCheckBig}
                  title={decided ? "Nothing decided yet" : "Nothing is waiting for you"}
                  body={
                    decided
                      ? "Approvals you approve or reject show up here."
                      : "Requests that need your approval show up here, and you are notified when one arrives."
                  }
                />
              }
            />
          </div>
        </TabsContent>
      </Tabs>
      {search.id && (
        <ApprovalSheet
          key={search.id}
          id={search.id}
          focusComment={focusComment}
          onClose={() => open(undefined)}
          onDecided={(a) => {
            if (a.status !== "pending") decidedId.current = a.id;
            open(undefined);
          }}
          returnFocus={nextInList}
        />
      )}
    </Page>
  );
}
