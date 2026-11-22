import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouter } from "@tanstack/react-router";
import { SquareCheckBig } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { Trans, useTranslation } from "react-i18next";
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
import { Field, Input, Textarea } from "@/components/ui/input";
import { openerOf } from "@/components/ui/opener";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { Approval, ApprovalEditableField, ApprovalSummary } from "@/lib/api/types";
import { useNow } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, toText } from "@/lib/utils";
import { formatList, relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";

export interface ApprovalsSearch {
  /** The approval open in the side panel. */
  id?: string;
  tab?: "decided";
}

type Decision = "approve" | "reject";

function levelText(a: ApprovalSummary) {
  if (a.step_key) return i18n.t("approvals:level.stepAsks", { name: a.level.name });
  return a.level.count > 1
    ? i18n.t("approvals:level.of", { name: a.level.name, index: a.level.index, total: a.level.count })
    : a.level.name;
}

function columnsFor(t: TFunction<"approvals">, decided: boolean): Column<ApprovalSummary>[] {
  return [
    {
      id: "request",
      header: t("columns.request"),
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
      header: t("columns.why"),
      cell: (a) => <span className="text-secondary">{a.step_key ? t("columns.stepWaits") : a.reasons.join("; ")}</span>,
    },
    {
      id: "requester",
      header: t("columns.requestedBy"),
      sortValue: (a) => a.requester.name,
      cell: (a) => (
        <>
          {a.requester.name}
          <div className="text-caption text-muted">{a.project.name}</div>
        </>
      ),
    },
    { id: "env", header: t("columns.environment"), cell: (a) => <EnvBadge env={a.environment} /> },
    {
      id: "submitted",
      header: t("columns.submitted"),
      sortValue: (a) => a.submitted_at,
      cell: (a) => (
        <span className="text-secondary text-muted">
          <Time iso={a.submitted_at} />
        </span>
      ),
    },
    decided
      ? { id: "status", header: t("columns.decision"), cell: (a) => <ApprovalStatusBadge status={a.status} /> }
      : {
          id: "due",
          header: t("columns.due"),
          sortValue: (a) => a.due_at,
          cell: (a) =>
            a.overdue ? (
              <span className="font-medium text-bad">{t("columns.overdue")}</span>
            ) : a.due_at ? (
              <span className="tnum text-secondary">{relative(a.due_at)}</span>
            ) : (
              <span className="text-secondary text-muted">{t("columns.noDeadline")}</span>
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
        decision === "approve"
          ? a.status === "approved"
            ? "decided.approved"
            : "decided.recorded"
          : "decided.rejected";
      toast.success(i18n.t(`approvals:${what}`, { number: a.request.number }), {
        description: i18n.t(a.status === "pending" ? "approvals:decided.levelWaits" : "approvals:decided.told"),
      });
      onDecided?.(a);
    },
    onError: (e) => {
      const commentErr = isApiError(e) ? e.problem.errors?.find((f) => f.field === "/comment") : undefined;
      if (commentErr && onCommentError) {
        onCommentError(commentErr.message);
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : i18n.t("approvals:decided.notSaved"), {
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
  const { t } = useTranslation("approvals");
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
                  {o.value === field.value && <span className="text-faint"> · {t("facts.requested")}</span>}
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
      <Input
        id={id}
        type="number"
        className="w-32"
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
  const { t } = useTranslation("approvals");
  return (
    <>
      <div className="flex flex-wrap items-center gap-1.5 text-secondary text-muted">
        <span className="font-mono">{a.request.number}</span> · {a.requester.name} · {a.project.name} ·{" "}
        <Time iso={a.submitted_at} /> <EnvBadge env={a.environment} production={a.environment === "prd"} />
      </div>
      {a.step_key ? (
        <div className="rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-body">
          <span className="font-medium">{t(a.can_decide ? "facts.stepWaitsForYou" : "facts.stepWaitsForPerson")}</span>
          {a.question?.message && <p className="mt-0.5 whitespace-pre-wrap">{a.question.message}</p>}
        </div>
      ) : (
        <div className="rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-body">
          <Trans
            t={t}
            i18nKey={a.can_decide ? "facts.whyNeedsYou" : "facts.whyNeedsApproval"}
            values={{ reasons: a.reasons.join("; ") }}
            components={{ label: <span className="font-medium" /> }}
          />
          {a.rules.length > 0 && (
            <span className="mt-0.5 block font-mono text-caption break-words text-muted">{a.rules.join("  ·  ")}</span>
          )}
        </div>
      )}
      {a.facts.length > 0 && <KeyValues items={a.facts.map((f) => ({ label: f.label, value: f.value }))} />}
      {a.justification && (
        <div className="flex flex-col gap-0.5 text-body">
          <span className="text-secondary font-medium text-muted">{t("facts.requesterReason")}</span>
          <p>{a.justification}</p>
        </div>
      )}
      {a.status === "pending" && a.due_at && (
        <p className="text-secondary text-muted">
          <Trans t={t} i18nKey="facts.due" components={{ time: <Time iso={a.due_at} /> }} />
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
  const { t } = useTranslation(["approvals", "common"]);
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
  // A panel that opened while its approval loaded has the focus on Close: the first field takes it once loaded.
  const loaded = Boolean(a);
  useEffect(() => {
    const active = document.activeElement;
    const dialog = active instanceof HTMLButtonElement ? active.closest('[role="dialog"]') : null;
    if (!loaded || !dialog) return;
    dialog.querySelector<HTMLElement>("input:not([disabled]), textarea:not([disabled])")?.focus();
  }, [loaded]);

  const changed = (a?.editable_fields ?? []).filter((f) => f.field in edits && edits[f.field] !== f.value);
  const changeLabel = formatList(
    changed.map((f) => f.options?.find((o) => o.value === edits[f.field])?.label ?? toText(edits[f.field])),
  );
  const send = (decision: Decision) => {
    if (decision === "reject" && !comment.trim()) {
      setCommentError(t("sheet.rejectWhy"));
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
        {t("sheet.openRequest")}
      </Link>
    </Button>
  );

  return (
    <Sheet
      open
      onOpenChange={(open) => !open && onClose()}
      pending={decide.isPending}
      title={a?.request.title ?? t("sheet.title")}
      description={a ? levelText(a) : undefined}
      returnFocus={returnFocus}
      footer={
        a &&
        (deciding ? (
          <>
            {openRequest}
            <Button variant="danger" disabled={decide.isPending} onClick={() => send("reject")}>
              {t("sheet.reject")}
            </Button>
            <Button variant="primary" disabled={decide.isPending} onClick={() => send("approve")}>
              {changed.length ? t("sheet.approveWith", { changes: changeLabel }) : t("sheet.approve")}
            </Button>
          </>
        ) : a.can_decide && a.step_key ? (
          <Button variant="primary" asChild>
            <Link to="/requests/$requestId" params={{ requestId: a.request.number }}>
              {t("sheet.answerOnRequest")}
            </Link>
          </Button>
        ) : (
          openRequest
        ))
      }
    >
      {query.isPending ? (
        <div className="flex flex-col gap-3" role="status" aria-label={t("common:loading")}>
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
              <Field label={t("sheet.comment")} htmlFor={commentId} hint={t("sheet.commentHint")} error={commentError}>
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
  const { t } = useTranslation("approvals");
  const navigate = useNavigate();
  const why = t(a.requester.id === meId ? "row.ownRequest" : "row.notYours");
  const reason = !a.can_decide && <span className="text-caption leading-snug text-faint">{why}</span>;
  return (
    <div className="min-w-56">
      <MenuItem onSelect={onReview}>{t("row.review")}</MenuItem>
      {a.status === "pending" &&
        (a.step_key ? (
          <MenuItem
            disabled={!a.can_decide}
            onSelect={() => void navigate({ to: "/requests/$requestId", params: { requestId: a.request.number } })}
          >
            <span className="flex flex-col">
              {t("sheet.answerOnRequest")}
              {reason}
            </span>
          </MenuItem>
        ) : (
          <>
            <MenuItem disabled={!a.can_decide} onSelect={onApprove}>
              <span className="flex flex-col">
                {t("sheet.approve")}
                {reason}
              </span>
            </MenuItem>
            <MenuItem disabled={!a.can_decide} danger onSelect={onReject}>
              <span className="flex flex-col">
                {t("row.reject")}
                {reason}
              </span>
            </MenuItem>
          </>
        ))}
      <MenuSeparator />
      <MenuItem onSelect={() => void navigate({ to: "/requests/$requestId", params: { requestId: a.request.number } })}>
        {t("sheet.openRequest")}
      </MenuItem>
    </div>
  );
}

export function ApprovalsPage({ search }: { search: ApprovalsSearch }) {
  const { t } = useTranslation("approvals");
  useNow(30_000);
  const navigate = useNavigate({ from: "/approvals" });
  const router = useRouter();
  const { me } = useCurrentProject();
  const decided = search.tab === "decided";
  const list = useQuery(q.approvals(decided ? ["approved", "rejected"] : ["pending"]));
  const listRef = useRef<HTMLDivElement>(null);
  // The approval whose comment takes the focus: Reject… from its row menu.
  const [focusCommentOf, setFocusCommentOf] = useState<string>();
  // Set once a decision removes its row: the focus goes to the next one.
  const decidedId = useRef<string | null>(null);
  // A row menu is gone by the time the panel opens: the focus goes back to its button.
  const menuOpener = useRef<HTMLElement | null>(null);
  // The panel opened over the list in this page's history entry: closing it steps back, so Back doesn't reopen it.
  const [openedOverList, setOpenedOverList] = useState(false);
  const [shownId, setShownId] = useState(search.id);
  if (shownId !== search.id) {
    setShownId(search.id);
    setOpenedOverList(Boolean(search.id && !shownId));
  }

  const open = (id: string, comment = false) => {
    menuOpener.current = openerOf(document.activeElement);
    setFocusCommentOf(comment ? id : undefined);
    void navigate({ search: (s: ApprovalsSearch) => ({ ...s, id }) });
  };
  const close = () => {
    if (openedOverList) router.history.back();
    else void navigate({ search: (s: ApprovalsSearch) => ({ ...s, id: undefined }), replace: true });
  };
  const nextInList = () => {
    const gone = decidedId.current;
    decidedId.current = null;
    const opener = menuOpener.current;
    menuOpener.current = null;
    if (!gone) return opener?.isConnected ? opener : null;
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
        title={t("page.title")}
        description={t("page.lead")}
        meta={
          total !== undefined && (
            <span className="tnum">{t(decided ? "page.decidedCount" : "page.waitingCount", { count: total })}</span>
          )
        }
      />
      <Tabs
        value={decided ? "decided" : "waiting"}
        onValueChange={(v) => void navigate({ search: v === "decided" ? { tab: "decided" } : {}, replace: true })}
      >
        <TabsList aria-label={t("page.tabs")}>
          <TabsTrigger value="waiting">{t("page.waiting")}</TabsTrigger>
          <TabsTrigger value="decided">{t("page.decided")}</TabsTrigger>
        </TabsList>
        <TabsContent value={decided ? "decided" : "waiting"} className="pt-4">
          <Panel flush>
            <div ref={listRef}>
              <DataTable
                label={t(decided ? "page.decidedTable" : "page.waiting")}
                columns={columnsFor(t, decided)}
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
                rowMenuLabel={(a) => t("row.menu", { number: a.request.number })}
                minWidth={900}
                loading={list.isPending}
                error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
                empty={
                  <EmptyState
                    icon={SquareCheckBig}
                    title={t(decided ? "page.nothingDecided" : "page.nothingWaiting")}
                    body={t(decided ? "page.nothingDecidedBody" : "page.nothingWaitingBody")}
                  />
                }
              />
            </div>
          </Panel>
        </TabsContent>
      </Tabs>
      {search.id && (
        <ApprovalSheet
          key={search.id}
          id={search.id}
          focusComment={focusCommentOf === search.id}
          onClose={close}
          onDecided={(a) => {
            if (a.status !== "pending") decidedId.current = a.id;
            close();
          }}
          returnFocus={nextInList}
        />
      )}
    </Page>
  );
}
