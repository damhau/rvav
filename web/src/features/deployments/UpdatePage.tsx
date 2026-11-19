import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { Page } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { acceptedWarnings, onlyWarned, PolicyWarnings, refusedWarnings } from "@/components/common/warnings";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { approvalBody, approvalTitle, quotaLabel } from "@/features/requests/RequestFormPage";
import { FormRenderer, shownFields, type FormHandle } from "@/forms/FormRenderer";
import { Attention, fieldControl, type Unplaced } from "@/forms/attention";
import { defaulted, sentInputs } from "@/forms/defaults";
import { mapFieldErrors, type MappedErrors } from "@/forms/errors";
import { sendingText, useSendingFiles } from "@/forms/file";
import { useSecretStandIns } from "@/forms/secretKeys";
import { api, call, isApiError } from "@/lib/api/client";
import type { CatalogItem, DeploymentDetail, RequestForm, UpdateDraft, UpdatePreview } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { keys, q } from "@/lib/query";
import { cn, newIdempotencyKey } from "@/lib/utils";

const masked = "••••";

/**
 * The form's values to start from: the deployment's inputs, but not its
 * secrets, which are given again. On its own version, a field it has no
 * value for was emptied, and stays empty rather than take its default; a
 * newer version's new fields start as on a new request, as do those of a
 * deployment adopted without inputs.
 */
export function startValues(form: RequestForm, d: DeploymentDetail): Record<string, unknown> {
  const props = (form.schema as { properties?: Record<string, unknown> }).properties ?? {};
  const fields = form.ui_schema.fields;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(d.inputs)) if (k in props && v !== masked && !fields[k]?.secret) out[k] = v;
  const fresh = form.version_id !== d.version.id || Object.keys(d.inputs).length === 0;
  for (const k of Object.keys(props)) {
    if (k in d.inputs) continue;
    const ui = fields[k];
    if (!fresh && ui && defaulted(ui) && !ui.secret) out[k] = null;
    else if (fresh && k in form.initial_values) out[k] = form.initial_values[k];
  }
  return out;
}

/** Why a checked update can't go on to its confirmation; null when it can. */
export function blockedBy(p: UpdatePreview, name: string): { title: string; reasons: string[] } | null {
  if (!p.allowed) return { title: p.reason ?? `${name} can't be updated now.`, reasons: [] };
  if (p.refused.length > 0)
    return {
      title: "These changes can't be made in place. Change them back, or delete the deployment and request it again.",
      reasons: p.refused.map((r) => r.reason),
    };
  if (p.unchanged)
    return { title: `${name} already has this version and these inputs: change an input to update it.`, reasons: [] };
  return null;
}

/** "3.1 → 3.2", or "3.2" when the version stays. */
function versionLine(p: UpdatePreview) {
  return p.from_version.id === p.to_version.id
    ? `${p.to_version.number}, unchanged`
    : `${p.from_version.number} → ${p.to_version.number}`;
}

function Summary({
  preview,
  error,
  onRetry,
  fetching,
  formErrors,
  pending,
  onReview,
  holding,
}: {
  preview: UpdatePreview | undefined;
  error: unknown;
  onRetry: () => void;
  fetching: boolean;
  formErrors: string[];
  pending: boolean;
  onReview: () => void;
  /** Why the update can't be reviewed yet: a file still sending. */
  holding?: string;
}) {
  const blocked = !preview || !preview.allowed || preview.refused.length > 0 || preview.unchanged;
  return (
    <div className="lg:sticky lg:top-4" aria-busy={fetching}>
      <Panel
        title="What the update does"
        flush
        actions={fetching && <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label="Updating" />}
        footer={
          <>
            {holding && (
              <span role="status" className="mr-auto text-caption text-muted">
                {holding}
              </span>
            )}
            <Button
              variant="primary"
              type="button"
              onClick={onReview}
              disabled={blocked || pending || Boolean(holding)}
            >
              {pending && <LoaderCircle className="animate-spin" aria-hidden />}
              Review the update
            </Button>
          </>
        }
      >
        {!preview && error ? (
          <ErrorState error={error} onRetry={onRetry} />
        ) : !preview ? (
          <div className="flex flex-col gap-2 p-4">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        ) : (
          <>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5 px-4 py-3">
              <dt className="text-muted">Version</dt>
              <dd className="m-0 font-mono text-secondary">{versionLine(preview)}</dd>
            </dl>
            {!preview.allowed && (
              <p role="status" className="border-t border-line bg-warn-soft px-4 py-3 text-warn">
                {preview.reason}
              </p>
            )}
            {preview.refused.length > 0 && (
              <div className="flex flex-col gap-1.5 border-t border-line bg-bad-soft px-4 py-3">
                <span className="font-medium text-bad">Can't be changed in place</span>
                <ul aria-label="Refused" className="flex list-disc flex-col gap-1 pl-4 text-secondary">
                  {preview.refused.map((r, i) => (
                    <li key={`${r.component ?? ""}-${r.setting ?? ""}-${i}`}>{r.reason}</li>
                  ))}
                </ul>
                <span className="text-secondary text-muted">
                  Change them back, or delete the deployment and request it again.
                </span>
              </div>
            )}
            <div className="flex flex-col gap-2 border-t border-line px-4 py-3">
              <span className="label">Changes</span>
              {preview.changes.length === 0 ? (
                <span className="text-secondary text-muted">
                  {preview.unchanged
                    ? "Change an input to see what the update does."
                    : "Nothing changes on the VMs: the deployment keeps the new inputs."}
                </span>
              ) : (
                <ol aria-label="Changes" className="flex flex-col gap-2">
                  {preview.changes.map((c, i) => (
                    <li key={`${c.kind}-${c.vm ?? ""}-${i}`} className="flex flex-col gap-0.5">
                      <span className="flex flex-wrap items-baseline gap-1.5">
                        <span className={cn("font-medium", c.destructive && "text-bad")}>{c.title}</span>
                        {c.destructive && <Badge tone="bad">Deletes</Badge>}
                      </span>
                      {c.detail && <span className="text-secondary text-muted">{c.detail}</span>}
                    </li>
                  ))}
                </ol>
              )}
            </div>
            {preview.quota.length > 0 && (
              <div className="flex flex-col gap-2.5 border-t border-line px-4 py-3">
                <span className="label">Quota after this update</span>
                {preview.quota.map((l) => {
                  const [name, unit] = quotaLabel[l.resource];
                  const frees = l.adding < 0;
                  return (
                    <Meter
                      key={`${l.resource}-${l.environment ?? ""}`}
                      label={`${name}${l.environment ? ` in ${l.environment}` : ""}${
                        frees ? ` · frees ${-l.adding}${unit ? ` ${unit}` : ""}` : ""
                      }${l.exceeds && l.enforcement === "soft" ? " · over the soft limit" : ""}`}
                      unit={unit}
                      used={frees ? l.used + l.adding : l.used}
                      adding={frees ? 0 : l.adding}
                      total={l.limit}
                    />
                  );
                })}
              </div>
            )}
            <div
              className={cn(
                "flex flex-col gap-0.5 border-t border-line px-4 py-3",
                preview.approval.required ? "bg-warn-soft text-warn" : "bg-ok-soft text-ok",
              )}
            >
              <span className="font-medium">
                {preview.approval.required ? approvalTitle(preview.approval) : "No approval needed"}
              </span>
              <span className="text-secondary">
                {preview.approval.required
                  ? approvalBody(preview.approval)
                  : "The update starts as soon as you confirm it."}
              </span>
            </div>
            {Boolean(preview.warnings?.length) && (
              <div className="border-t border-line px-4 py-3">
                <PolicyWarnings
                  warnings={preview.warnings ?? []}
                  note="You may update anyway: the request keeps the warning."
                />
              </div>
            )}
          </>
        )}
        {formErrors.length > 0 && (
          <ul
            role="alert"
            className="m-4 flex flex-col gap-1 rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary text-bad"
          >
            {formErrors.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}

function UpdateBody({ d, form }: { d: DeploymentDetail; form: RequestForm }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [values, setValues] = useState(() => startValues(form, d));
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [submitted, setSubmitted] = useState(false);
  const [serverErrors, setServerErrors] = useState<MappedErrors | null>(null);
  // Counts the attempts with problems: each moves the focus to the first one to fix.
  const [refusals, setRefusals] = useState(0);
  const [justification, setJustification] = useState("");
  // The update as Review checked it, while the dialog asks to confirm it; refused counts the refusals
  // that only warned, each with the warnings found then.
  const [confirm, setConfirm] = useState<{ preview: UpdatePreview; body: UpdateDraft; refused?: number } | null>(null);
  // Why Review stopped short of the confirmation, and how often: each time moves the focus to it.
  const [blocked, setBlocked] = useState<{ title: string; reasons: string[]; at: number } | null>(null);
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey);
  const formRef = useRef<FormHandle>(null);
  const attentionRef = useRef<HTMLDivElement>(null);
  const blockedRef = useRef<HTMLDivElement>(null);
  const fields = form.ui_schema.fields;

  const body = useMemo(
    () => ({ version_id: form.version_id, inputs: sentInputs(fields, values) }),
    [form.version_id, fields, values],
  );
  const debounced = useDebounced(body, 350);
  const standIns = useSecretStandIns(fields);
  const previewKey = (b: UpdateDraft) => keys.deployments.updatePreview(d.id, { ...b, inputs: standIns(b.inputs) });
  // The cached queries keep their queryFn: it reads the draft here, so none keeps a password of its own.
  const sending = useRef(debounced);
  sending.current = debounced;
  // previewKey holds the draft, its secrets as stand-ins.
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const preview = useQuery({
    queryKey: previewKey(debounced),
    queryFn: ({ signal }) =>
      call(
        api.POST("/deployments/{deploymentId}/update/preview", {
          params: { path: { deploymentId: d.id } },
          body: sending.current,
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
  });
  const p = preview.data;

  const submit = useMutation({
    // Its variables hold the secret inputs: gone from the mutation cache once done.
    gcTime: 0,
    mutationFn: (draft: UpdateDraft) =>
      call(
        api.POST("/deployments/{deploymentId}/update", {
          params: { path: { deploymentId: d.id }, header: { "Idempotency-Key": idempotencyKey } },
          body: draft,
        }),
      ),
    onSuccess: (r) => {
      setIdempotencyKey(newIdempotencyKey());
      setConfirm(null);
      void qc.invalidateQueries({ queryKey: keys.deployments.detail(d.id) });
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(d.id) });
      void qc.invalidateQueries({ queryKey: keys.deployments.history(d.id) });
      void qc.invalidateQueries({ queryKey: keys.requests.lists() });
      void qc.invalidateQueries({ queryKey: keys.vms.all });
      toast.success(`${r.number} submitted`, {
        description: r.status === "pending_approval" ? "Waiting for approval." : "The update has started.",
      });
      void navigate({ to: "/requests/$requestId", params: { requestId: r.number } });
    },
    onError: (e) => {
      if (onlyWarned(e)) {
        // A policy changed since Review: the dialog asks again, with the warnings found now.
        const warnings = refusedWarnings(e);
        setConfirm(
          (c) =>
            c && {
              preview: { ...c.preview, warnings },
              body: { ...c.body, accepted_warnings: acceptedWarnings(warnings) },
              refused: (c.refused ?? 0) + 1,
            },
        );
        void qc.invalidateQueries({ queryKey: ["deployments", "update-preview", d.id] });
        return;
      }
      setConfirm(null);
      setSubmitted(true);
      if (isApiError(e) && e.status === 422) {
        const mapped = mapFieldErrors(e.fieldErrors);
        if (Object.keys(mapped.fields).length === 0 && mapped.form.length === 0)
          mapped.form.push(e.problem.detail ?? e.problem.title);
        setServerErrors(mapped);
        setRefusals((n) => n + 1);
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : "The update was not sent", {
        description: isApiError(e) ? `${e.problem.detail ?? ""} Request ID ${e.requestId}`.trim() : undefined,
      });
    },
  });

  const previewErrors = mapFieldErrors(p?.errors);
  const fieldErrors: Record<string, string> = {};
  for (const [name, msg] of Object.entries(previewErrors.fields))
    if (submitted || touched.has(name)) fieldErrors[name] = msg;
  for (const [name, msg] of Object.entries(serverErrors?.fields ?? {})) if (msg) fieldErrors[name] = msg;
  const formErrors = submitted ? [...new Set([...(serverErrors?.form ?? []), ...previewErrors.form])] : [];

  const placed = shownFields(form, p?.fields);
  const attention = submitted ? Object.keys(fieldErrors) : [];
  const unplaced: Unplaced[] = attention
    .filter((name) => !placed.includes(name))
    .map((name) => ({ key: name, label: name, code: true, message: fieldErrors[name]! }));
  const focusFirst = () => {
    const name = placed.find((n) => fieldErrors[n] && fieldControl(n));
    if (name) return formRef.current?.focus(name);
    attentionRef.current?.focus();
  };
  const focusFirstRef = useRef(focusFirst);
  focusFirstRef.current = focusFirst;
  useEffect(() => {
    if (refusals) focusFirstRef.current();
  }, [refusals]);
  useEffect(() => {
    if (blocked) blockedRef.current?.focus();
  }, [blocked]);

  const setValue = (name: string, value: unknown) => {
    setBlocked(null);
    setValues((v) => (Object.hasOwn(v, name) && v[name] === value ? v : { ...v, [name]: value }));
    if (serverErrors?.fields[name]) setServerErrors((s) => s && { ...s, fields: { ...s.fields, [name]: "" } });
  };
  // The summary may still show the values before the last keystroke: Review checks these.
  const review = async () => {
    const checking = body;
    let checked: UpdatePreview;
    try {
      // eslint-disable-next-line @tanstack/query/exhaustive-deps
      checked = await qc.fetchQuery({
        queryKey: previewKey(checking),
        queryFn: ({ signal }) =>
          call(
            api.POST("/deployments/{deploymentId}/update/preview", {
              params: { path: { deploymentId: d.id } },
              body: checking,
              signal,
            }),
          ),
      });
    } catch (e) {
      toast.error(isApiError(e) ? e.problem.title : "The update couldn't be checked", {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
      return;
    }
    if (checked.errors.length) {
      setSubmitted(true);
      setRefusals((n) => n + 1);
      return;
    }
    const why = blockedBy(checked, d.name);
    if (why) {
      setBlocked({ ...why, at: Date.now() });
      return;
    }
    setBlocked(null);
    setConfirm({
      preview: checked,
      body: {
        ...checking,
        ...(justification ? { justification } : {}),
        accepted_warnings: acceptedWarnings(checked.warnings ?? []),
      },
    });
  };
  const holding = sendingText(useSendingFiles());
  const secrets = Object.entries(d.inputs).some(([k, v]) => v === masked && fields[k]?.secret);
  const reason = (
    <Panel as="fieldset" title="Reason">
      <Field label="Reason" htmlFor="justification" hint="Approvers read it.">
        <Textarea
          id="justification"
          rows={2}
          placeholder="Why does it change? Add a ticket number if there is one."
          value={justification}
          onChange={(e) => setJustification(e.target.value)}
        />
      </Field>
    </Panel>
  );
  const note = blocked && (
    <div
      ref={blockedRef}
      role="alert"
      tabIndex={-1}
      className="flex flex-col gap-1.5 rounded-lg border border-bad/40 bg-bad-soft px-4 py-3 text-secondary outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <span className="font-medium text-bad">{blocked.title}</span>
      {blocked.reasons.length > 0 && (
        <ul className="flex list-disc flex-col gap-1 pl-4">
          {blocked.reasons.map((r, i) => (
            <li key={`${r}-${i}`}>{r}</li>
          ))}
        </ul>
      )}
    </div>
  );

  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
        {attention.length > 0 && <Attention ref={attentionRef} count={attention.length} unplaced={unplaced} />}
        {secrets && (
          <p className="rounded-lg border border-line bg-sunken px-4 py-3 text-secondary">
            rvav doesn't show the secret inputs: give them again.
          </p>
        )}
        <FormRenderer
          form={form}
          values={values}
          onChange={setValue}
          onBlur={(name) => setTouched((t) => (t.has(name) ? t : new Set(t).add(name)))}
          errors={fieldErrors}
          ctx={{ versionId: form.version_id, projectId: d.project.id, environment: d.environment }}
          fieldStates={p?.fields}
          ref={formRef}
          wizard={{
            review: (
              <>
                {reason}
                {note}
              </>
            ),
            submit: (
              <Button
                variant="primary"
                type="button"
                onClick={() => void review()}
                disabled={submit.isPending || Boolean(holding)}
              >
                Review the update
              </Button>
            ),
            holding,
          }}
        />
        {form.ui_schema.layout.kind !== "wizard" && (
          <>
            {reason}
            {note}
          </>
        )}
      </div>
      <aside className="flex min-w-0 flex-[1_1_320px] flex-col self-stretch">
        <Summary
          preview={p}
          error={preview.error}
          onRetry={() => void preview.refetch()}
          fetching={preview.isFetching}
          formErrors={formErrors}
          pending={submit.isPending}
          onReview={() => void review()}
          holding={holding}
        />
      </aside>
      {confirm && (
        <ConfirmUpdate
          name={d.name}
          preview={confirm.preview}
          refused={confirm.refused}
          pending={submit.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => submit.mutate(confirm.body)}
        />
      )}
    </div>
  );
}

/** Asks before the update runs: what it deletes, or what it changes. */
function ConfirmUpdate({
  name,
  preview: p,
  refused,
  pending,
  onCancel,
  onConfirm,
}: {
  name: string;
  preview: UpdatePreview;
  /** Counts the refusals that only warned: each moves the focus to the warnings. */
  refused?: number;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const destructive = p.changes.filter((c) => c.destructive);
  const what = p.changes.length === 1 ? "this change" : `these ${p.changes.length} changes`;
  const rest = p.changes.length - destructive.length;
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && onCancel()}
      title={`Update ${name}?`}
      description={
        p.changes.length === 0
          ? "Nothing changes on the VMs: the deployment keeps the new inputs."
          : p.approval.required
            ? `It waits for approval, then makes ${what}.`
            : `rvav makes ${what} now.`
      }
      consequences={
        (p.changes.length > 0 || Boolean(p.warnings?.length)) && (
          <div className="flex flex-col gap-2">
            {p.changes.length > 0 && (
              <ul className="flex list-disc flex-col gap-0.5 pl-4">
                {(destructive.length ? destructive : p.changes).map((c, i) => (
                  <li key={`${c.kind}-${c.vm ?? ""}-${i}`}>
                    {c.title}
                    {c.destructive && c.detail ? `: ${c.detail.charAt(0).toLowerCase()}${c.detail.slice(1)}` : ""}
                  </li>
                ))}
                {destructive.length > 0 && rest > 0 && (
                  <li>
                    and {rest} more change{rest === 1 ? "" : "s"}
                  </li>
                )}
              </ul>
            )}
            <PolicyWarnings warnings={p.warnings ?? []} note="" refusal={refused} />
          </div>
        )
      }
      confirmLabel={`${p.approval.required ? "Submit for approval" : "Update"}${p.warnings?.length ? " anyway" : ""}`}
      cancelLabel="Keep editing"
      tone={destructive.length ? "danger" : "primary"}
      pending={pending}
      onConfirm={onConfirm}
    />
  );
}

export function UpdatePage({ deploymentId }: { deploymentId: string }) {
  const dep = useQuery(q.deployment(deploymentId));
  const d = dep.data;
  const slug = d?.catalog_item.slug ?? "";
  const projectId = d?.project.id ?? "";
  const item = useQuery({ ...q.catalogItem(slug, projectId), enabled: Boolean(d) });
  const form = useQuery({ ...q.form(slug, projectId), enabled: Boolean(d) });
  const error = dep.error ?? item.error ?? form.error;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/deployments">Deployments</Link>
            <Link to="/deployments/$deploymentId" params={{ deploymentId }}>
              {d?.name ?? "…"}
            </Link>
          </Breadcrumbs>
        }
        title={d ? `Update ${d.name}` : <Skeleton className="h-5 w-56" />}
        description="Change its inputs, or move it to the item's new version. Review what changes before it runs."
        meta={d && <UpdateMeta d={d} item={item.data} />}
      />
      {error ? (
        <ErrorState error={error} className="rounded-lg border border-line bg-surface" />
      ) : !d || !form.data ? (
        <div className="flex flex-wrap gap-4">
          <Skeleton className="h-64 flex-[999_1_560px] rounded-lg" />
          <Skeleton className="h-64 flex-[1_1_320px] rounded-lg" />
        </div>
      ) : (
        <UpdateBody key={form.data.version_id} d={d} form={form.data} />
      )}
    </Page>
  );
}

function UpdateMeta({ d, item }: { d: DeploymentDetail; item?: CatalogItem }) {
  return (
    <>
      <span className="flex items-center gap-1.5">
        {d.project.name} <EnvBadge env={d.environment} />
      </span>
      <span>
        {d.catalog_item.name} {d.version.number}
        {item && item.version.id !== d.version.id ? ` · version ${item.version.number} is out` : ""}
      </span>
    </>
  );
}
