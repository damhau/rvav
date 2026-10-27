import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useRef, useState } from "react";
import { toast } from "sonner";
import { ApprovalHistory, dueText } from "@/components/common/approvals";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { EmptyState, KeyValues, Page, PageHeader } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Field, Textarea } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { Approval, ApprovalEditableField, ApprovalSummary } from "@/lib/api/types";
import { useNow } from "@/lib/hooks";
import { keys, q } from "@/lib/query";
import { cn, relative, toText } from "@/lib/utils";

export interface ApprovalsSearch {
  /** The selected approval. */
  id?: string;
  tab?: "decided";
}

function who(a: ApprovalSummary) {
  return `${a.requester.name} · ${a.project.name} · ${relative(a.submitted_at)}`;
}

function ListItem({ a, selected, onSelect }: { a: ApprovalSummary; selected: boolean; onSelect: () => void }) {
  return (
    <li>
      <button
        type="button"
        aria-pressed={selected}
        onClick={onSelect}
        className={cn(
          "flex w-full flex-col gap-0.5 border-b border-line px-4 py-2.5 text-left hover:bg-sunken",
          selected && "bg-accent-soft hover:bg-accent-soft",
        )}
      >
        <span className="flex items-center justify-between gap-2 text-xs">
          <span className="font-mono text-muted">{a.request.number}</span>
          {a.status === "pending" && a.due_at ? (
            <span className={cn("tnum", a.overdue ? "font-medium text-bad" : "text-faint")}>
              {a.overdue ? "Overdue" : `Due ${relative(a.due_at)}`}
            </span>
          ) : (
            <span className="text-faint">{a.status === "pending" ? "" : a.status}</span>
          )}
        </span>
        <span className="text-[13px] font-medium">{a.request.title}</span>
        <span className="text-xs text-muted">{who(a)}</span>
      </button>
    </li>
  );
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
        <legend className="mb-1 text-xs font-medium">{field.label}</legend>
        <div className="grid gap-1.5 sm:grid-cols-2">
          {field.options.map((o) => (
            <label
              key={o.value}
              className={cn(
                "flex cursor-pointer items-start gap-2 rounded-md border border-line px-2.5 py-2 text-[13px] hover:bg-sunken",
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
                {o.meta && <span className="text-xs text-muted">{o.meta}</span>}
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
        className="h-8 w-32 rounded-md border border-line-strong bg-surface px-2.5 text-[13px]"
        min={field.minimum}
        max={field.maximum}
        value={toText(value)}
        onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
      />
    </Field>
  );
}

function DecisionForm({ approval, onDecided }: { approval: Approval; onDecided: () => void }) {
  const qc = useQueryClient();
  const commentId = useId();
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const [edits, setEdits] = useState<Record<string, unknown>>({});
  const [comment, setComment] = useState("");
  const [commentError, setCommentError] = useState<string | null>(null);

  const changed = approval.editable_fields.filter((f) => f.field in edits && edits[f.field] !== f.value);
  const changeLabel = changed
    .map((f) => f.options?.find((o) => o.value === edits[f.field])?.label ?? toText(edits[f.field]))
    .join(", ");

  const decide = useMutation({
    mutationFn: (decision: "approve" | "reject") =>
      call(
        api.POST("/approvals/{approvalId}/decision", {
          params: { path: { approvalId: approval.id } },
          body: {
            decision,
            comment: comment.trim() || undefined,
            inputs:
              decision === "approve" && changed.length
                ? Object.fromEntries(changed.map((f) => [f.field, edits[f.field]]))
                : undefined,
          },
        }),
      ),
    onSuccess: (a, decision) => {
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
      onDecided();
    },
    onError: (e) => {
      const commentErr = isApiError(e) ? e.problem.errors?.find((f) => f.field === "/comment") : undefined;
      if (commentErr) {
        setCommentError(commentErr.message);
        commentRef.current?.focus();
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : "The decision was not saved", {
        description: isApiError(e) ? (e.problem.errors?.[0]?.message ?? e.problem.detail) : undefined,
      });
    },
  });

  const reject = () => {
    if (!comment.trim()) {
      setCommentError("Say why you reject it: the requester sees this comment.");
      commentRef.current?.focus();
      return;
    }
    decide.mutate("reject");
  };

  return (
    <div className="flex flex-col gap-3.5 border-t border-line pt-3.5">
      {approval.editable_fields.map((f) => (
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
        aside={<span className="text-xs text-faint">Sent to the requester; required to reject</span>}
      >
        <Textarea
          id={commentId}
          ref={commentRef}
          value={comment}
          aria-invalid={commentError ? true : undefined}
          aria-describedby={commentError ? `${commentId}-error` : undefined}
          onChange={(e) => {
            setComment(e.target.value);
            setCommentError(null);
          }}
        />
        {commentError && (
          <p id={`${commentId}-error`} className="text-xs text-bad">
            {commentError}
          </p>
        )}
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" disabled={decide.isPending} onClick={() => decide.mutate("approve")}>
          {changed.length ? `Approve with ${changeLabel}` : "Approve"}
        </Button>
        <Button variant="danger" disabled={decide.isPending} onClick={reject}>
          Reject
        </Button>
      </div>
    </div>
  );
}

function ApprovalDetail({ id, onDecided }: { id: string; onDecided: () => void }) {
  const query = useQuery(q.approval(id));
  if (query.isPending) {
    return (
      <div className="flex flex-col gap-3 p-4" role="status" aria-label="Loading">
        <Skeleton className="h-4 w-48" />
        <Skeleton className="h-5 w-80" />
        <Skeleton className="h-28" />
      </div>
    );
  }
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  const a = query.data;
  return (
    <article className="flex flex-col gap-3.5 p-4" aria-label={`Approval for ${a.request.number}`}>
      <div className="flex flex-col gap-1">
        <span className="text-xs text-faint">
          <span className="font-mono">{a.request.number}</span> · {a.level.name}
          {a.level.count > 1 && ` · level ${a.level.index} of ${a.level.count}`}
        </span>
        <h2 className="text-base font-semibold">
          <Link to="/requests/$requestId" params={{ requestId: a.request.number }} className="text-fg">
            {a.request.title}
          </Link>
        </h2>
        <span className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
          {who(a)} <EnvBadge env={a.environment} production={a.environment === "prd"} />
        </span>
      </div>

      <div className="rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-[13px]">
        <span className="font-medium">{a.can_decide ? "Why this needs you" : "Why it needs approval"}: </span>
        {a.reasons.join("; ")}
        {a.rules.length > 0 && (
          <span className="mt-0.5 block font-mono text-[11px] break-words text-muted">{a.rules.join("  ·  ")}</span>
        )}
      </div>

      {a.facts.length > 0 && <KeyValues items={a.facts.map((f) => ({ label: f.label, value: f.value }))} />}

      {a.justification && (
        <div className="flex flex-col gap-0.5 text-[13px]">
          <span className="text-xs font-medium text-muted">Requester’s reason</span>
          <p>{a.justification}</p>
        </div>
      )}

      {a.can_decide ? (
        <DecisionForm key={a.id} approval={a} onDecided={onDecided} />
      ) : (
        <div className="border-t border-line pt-3.5">
          <ApprovalHistory levels={[a]} />
        </div>
      )}

      {a.status === "pending" && a.due_at && (
        <p className="text-xs text-faint">
          Due <Time iso={a.due_at} />
          {dueText(a) && ` · ${dueText(a)}`}
        </p>
      )}
    </article>
  );
}

export function ApprovalsPage({ search }: { search: ApprovalsSearch }) {
  useNow(30_000);
  const navigate = useNavigate({ from: "/approvals" });
  const decided = search.tab === "decided";
  const list = useQuery(q.approvals(decided ? ["approved", "rejected"] : ["pending"]));
  const items = list.data?.items ?? [];
  const selected = items.find((a) => a.id === search.id)?.id ?? search.id ?? items[0]?.id;
  const select = (id?: string) => void navigate({ search: (s: ApprovalsSearch) => ({ ...s, id }), replace: true });
  const next = () => {
    const rest = items.filter((a) => a.id !== selected);
    select(decided ? selected : rest[0]?.id);
  };

  return (
    <Page>
      <PageHeader
        title="Approvals"
        meta={<span>Requests that wait for your decision. Approving lets them start at once.</span>}
      />
      <Tabs
        value={decided ? "decided" : "waiting"}
        onValueChange={(v) => void navigate({ search: v === "decided" ? { tab: "decided" } : {} })}
      >
        <TabsList>
          <TabsTrigger value="waiting">Waiting for you</TabsTrigger>
          <TabsTrigger value="decided">Decided</TabsTrigger>
        </TabsList>
      </Tabs>
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(280px,360px)_minmax(0,1fr)]">
        <section
          aria-label={decided ? "Decided approvals" : "Waiting for you"}
          className="rounded-lg border border-line bg-surface"
        >
          <div className="flex items-center justify-between border-b border-line px-4 py-2.5 text-xs">
            <span className="font-semibold">
              {decided ? "Decided" : "Waiting for you"}
              {list.data && <span className="font-normal text-faint"> · {list.data.total}</span>}
            </span>
            <span className="text-faint">{decided ? "Newest first" : "Oldest first"}</span>
          </div>
          {list.isPending ? (
            <SkeletonRows rows={3} cols={2} />
          ) : list.isError ? (
            <ErrorState error={list.error} onRetry={() => void list.refetch()} />
          ) : items.length === 0 ? (
            <EmptyState
              title={decided ? "Nothing decided yet" : "Nothing is waiting for you"}
              body={
                decided
                  ? "Approvals you approve or reject show up here."
                  : "Requests that need your approval show up here, and you are notified when one arrives."
              }
            />
          ) : (
            <ul>
              {items.map((a) => (
                <ListItem key={a.id} a={a} selected={a.id === selected} onSelect={() => select(a.id)} />
              ))}
            </ul>
          )}
        </section>
        <section aria-label="Selected approval" className="min-w-0 rounded-lg border border-line bg-surface">
          {selected ? (
            <ApprovalDetail id={selected} onDecided={next} />
          ) : (
            <EmptyState title="No approval selected" body="Pick one in the list to see what it asks for." />
          )}
        </section>
      </div>
    </Page>
  );
}
