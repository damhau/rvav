import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
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
import { Select } from "@/components/ui/select";
import {
  approvalBody,
  approvalTitle,
  pickedVersion,
  refreshVersions,
  useFocusVersion,
} from "@/features/requests/RequestFormPage";
import { LimitMeters } from "@/features/requests/limits";
import { quotaLabel, quotaWhere, tightest } from "@/features/requests/quota";
import { FormRenderer, shownFields, type FormHandle } from "@/forms/FormRenderer";
import { Attention, fieldControl, type Unplaced } from "@/forms/attention";
import { defaulted, sentInputs } from "@/forms/defaults";
import { mapFieldErrors, type MappedErrors } from "@/forms/errors";
import { sendingText, useSendingFiles } from "@/forms/file";
import { useSecretStandIns } from "@/forms/secretKeys";
import { api, call, isApiError } from "@/lib/api/client";
import type {
  CatalogItem,
  DeploymentDetail,
  FieldError,
  RequestForm,
  UpdateDraft,
  UpdatePreview,
} from "@/lib/api/types";
import { useEnvName } from "@/lib/environments";
import { formatNumber, formatSize } from "@/lib/format";
import { useDebounced } from "@/lib/hooks";
import { i18n } from "@/lib/i18n";
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

/** What the form of another version starts from: the inputs changed here, and the reason. */
interface UpdateStart {
  inputs: Record<string, unknown>;
  justification: string;
  /** Why the update was refused, when it named a version since retired. */
  refusal?: MappedErrors;
}

/** The control that fixes an input's problem. */
const controlOf = (name: string) => (name === "$version_id" ? document.getElementById("version") : fieldControl(name));

/** Why a checked update can't go on to its confirmation; null when it can. */
export function blockedBy(p: UpdatePreview, name: string): { title: string; reasons: string[] } | null {
  if (!p.allowed) return { title: p.reason ?? i18n.t("deployments:update.cantNow", { name }), reasons: [] };
  if (p.refused.length > 0)
    return { title: i18n.t("deployments:update.notInPlace"), reasons: p.refused.map((r) => r.reason) };
  if (p.unchanged) return { title: i18n.t("deployments:update.unchanged", { name }), reasons: [] };
  return null;
}

/** "3.1 → 3.2", or "3.2" when the version stays. */
function versionLine(p: UpdatePreview) {
  return p.from_version.id === p.to_version.id
    ? i18n.t("deployments:update.versionSame", { number: p.to_version.number })
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
  const { t } = useTranslation("deployments");
  const envName = useEnvName();
  const blocked = !preview || !preview.allowed || preview.refused.length > 0 || preview.unchanged;
  return (
    <div className="lg:sticky lg:top-4" aria-busy={fetching}>
      <Panel
        title={t("update.summary")}
        flush
        actions={
          fetching && <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label={t("update.updating")} />
        }
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
              {t("update.review")}
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
              <dt className="text-muted">{t("details.version")}</dt>
              <dd className="m-0 font-mono text-secondary">{versionLine(preview)}</dd>
            </dl>
            {!preview.allowed && (
              <p role="status" className="border-t border-line bg-warn-soft px-4 py-3 text-warn">
                {preview.reason}
              </p>
            )}
            {preview.refused.length > 0 && (
              <div className="flex flex-col gap-1.5 border-t border-line bg-bad-soft px-4 py-3">
                <span className="font-medium text-bad">{t("update.refused")}</span>
                <ul aria-label={t("update.refusedList")} className="flex list-disc flex-col gap-1 pl-4 text-secondary">
                  {preview.refused.map((r, i) => (
                    <li key={`${r.component ?? ""}-${r.setting ?? ""}-${i}`}>{r.reason}</li>
                  ))}
                </ul>
                <span className="text-secondary text-muted">{t("update.changeBack")}</span>
              </div>
            )}
            <div className="flex flex-col gap-2 border-t border-line px-4 py-3">
              <span className="label">{t("update.changes")}</span>
              {preview.changes.length === 0 ? (
                <span className="text-secondary text-muted">
                  {preview.unchanged ? t("update.changeInput") : t("update.nothingChanges")}
                </span>
              ) : (
                <ol aria-label={t("update.changes")} className="flex flex-col gap-2">
                  {preview.changes.map((c, i) => (
                    <li key={`${c.kind}-${c.vm ?? ""}-${i}`} className="flex flex-col gap-0.5">
                      <span className="flex flex-wrap items-baseline gap-1.5">
                        <span className={cn("font-medium", c.destructive && "text-bad")}>{c.title}</span>
                        {c.destructive && <Badge tone="bad">{t("update.deletes")}</Badge>}
                      </span>
                      {c.detail && <span className="text-secondary text-muted">{c.detail}</span>}
                    </li>
                  ))}
                </ol>
              )}
            </div>
            {preview.quota.length > 0 && (
              <div className="flex flex-col gap-2.5 border-t border-line px-4 py-3">
                <span className="label">{t("update.quota")}</span>
                {tightest(preview.quota).map((l) => {
                  const [name, unit] = quotaLabel[l.resource];
                  const frees = l.adding < 0;
                  const amount = unit === "GB" ? formatSize(-l.adding, "GB") : formatNumber(-l.adding);
                  const freed = t("update.frees", { amount });
                  const soft = l.exceeds && l.enforcement === "soft";
                  return (
                    <Meter
                      key={l.resource}
                      label={`${name}${quotaWhere(l, envName)}${frees ? ` · ${freed}` : ""}${soft ? ` · ${t("update.overSoft")}` : ""}`}
                      unit={unit}
                      used={frees ? l.used + l.adding : l.used}
                      adding={frees ? 0 : l.adding}
                      total={l.limit}
                    />
                  );
                })}
              </div>
            )}
            <LimitMeters title={t("update.limits")} lines={preview.limits ?? []} />
            <div
              className={cn(
                "flex flex-col gap-0.5 border-t border-line px-4 py-3",
                preview.approval.required ? "bg-warn-soft text-warn" : "bg-ok-soft text-ok",
              )}
            >
              <span className="font-medium">
                {preview.approval.required ? approvalTitle(preview.approval) : t("update.noApproval")}
              </span>
              <span className="text-secondary">
                {preview.approval.required ? approvalBody(preview.approval) : t("update.startsOnConfirm")}
              </span>
            </div>
            {Boolean(preview.warnings?.length) && (
              <div className="border-t border-line px-4 py-3">
                <PolicyWarnings warnings={preview.warnings ?? []} note={t("update.mayAnyway")} />
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

function UpdateBody({
  d,
  form,
  item,
  start,
  onVersion,
  focusVersion,
}: {
  d: DeploymentDetail;
  form: RequestForm;
  item?: CatalogItem;
  start?: UpdateStart;
  /** Another version picked to update to: its form starts from the deployment's inputs, and those changed here. */
  onVersion: (versionId: string, keep: UpdateStart) => void;
  /** Set when the form remounts for a version picked: the focus goes back to Version. */
  focusVersion?: RefObject<boolean>;
}) {
  // Also renders the text sendingText writes again on a language switch.
  const { t } = useTranslation("deployments");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [values, setValues] = useState(() => {
    const props = (form.schema as { properties?: Record<string, unknown> }).properties ?? {};
    const kept = Object.entries(start?.inputs ?? {}).filter(([k]) => k in props);
    return { ...startValues(form, d), ...Object.fromEntries(kept) };
  });
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [submitted, setSubmitted] = useState(Boolean(start?.refusal));
  const [serverErrors, setServerErrors] = useState<MappedErrors | null>(start?.refusal ?? null);
  // Counts the attempts with problems: each moves the focus to the first one to fix.
  const [refusals, setRefusals] = useState(start?.refusal ? 1 : 0);
  const [justification, setJustification] = useState(start?.justification ?? "");
  // The update as Review checked it, while the dialog asks to confirm it; refused counts the refusals
  // that only warned, each with the warnings found then.
  const [confirm, setConfirm] = useState<{ preview: UpdatePreview; body: UpdateDraft; refused?: number } | null>(null);
  // Why Review stopped short of the confirmation, and how often: each time moves the focus to it.
  const [blocked, setBlocked] = useState<{ title: string; reasons: string[]; at: number } | null>(null);
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey);
  const formRef = useRef<FormHandle>(null);
  const attentionRef = useRef<HTMLDivElement>(null);
  const blockedRef = useRef<HTMLDivElement>(null);
  // The confirmation's update was refused: as the dialog closes, the focus stays on what to fix.
  const refused = useRef(false);
  const fields = form.ui_schema.fields;
  useFocusVersion(focusVersion, formRef);
  // The inputs changed here: another version's form starts from its own values, then these.
  const kept = (): UpdateStart => {
    const was = startValues(form, d);
    return {
      inputs: Object.fromEntries(Object.entries(values).filter(([k, v]) => !Object.is(was[k], v))),
      justification,
    };
  };
  // The version was retired since the page loaded: the item's versions are read again, and the form
  // of the one offered now starts from this one and says why.
  const retired = (errors: FieldError[]) => {
    if (!errors.some((e) => e.code === "stale_version")) return;
    onVersion(form.version_id, { ...kept(), refusal: mapFieldErrors(errors) });
    refreshVersions(qc, form.version_id);
  };

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
      toast.success(t("update.submitted", { number: r.number }), {
        description: r.status === "pending_approval" ? t("update.waiting") : t("update.started"),
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
        refused.current = true;
        const mapped = mapFieldErrors(e.fieldErrors);
        if (Object.keys(mapped.fields).length === 0 && mapped.form.length === 0)
          mapped.form.push(e.problem.detail ?? e.problem.title);
        setServerErrors(mapped);
        setRefusals((n) => n + 1);
        retired(e.fieldErrors);
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : t("update.notSent"), {
        description: isApiError(e)
          ? t("update.requestId", { detail: e.problem.detail ?? "", id: e.requestId }).trim()
          : undefined,
      });
    },
  });

  const previewErrors = mapFieldErrors(p?.errors);
  const fieldErrors: Record<string, string> = {};
  for (const [name, msg] of Object.entries(previewErrors.fields))
    if (submitted || touched.has(name)) fieldErrors[name] = msg;
  for (const [name, msg] of Object.entries(serverErrors?.fields ?? {})) if (msg) fieldErrors[name] = msg;
  const formErrors = submitted ? [...new Set([...(serverErrors?.form ?? []), ...previewErrors.form])] : [];

  const placed = [...(item?.versions ? ["$version_id"] : []), ...shownFields(form, p?.fields)];
  const attention = submitted ? Object.keys(fieldErrors) : [];
  const unplaced: Unplaced[] = attention
    .filter((name) => !placed.includes(name))
    .map((name) => ({
      key: name,
      ...(name === "$version_id" ? { label: t("details.version") } : { label: name, code: true }),
      message: fieldErrors[name]!,
    }));
  const focusFirst = () => {
    const name = placed.find((n) => fieldErrors[n] && controlOf(n));
    if (name) return formRef.current?.focus(name, () => controlOf(name));
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
      toast.error(isApiError(e) ? e.problem.title : t("update.notChecked"), {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
      return;
    }
    if (checked.errors.length) {
      setSubmitted(true);
      setRefusals((n) => n + 1);
      retired(checked.errors);
      return;
    }
    const why = blockedBy(checked, d.name);
    if (why) {
      setBlocked({ ...why, at: Date.now() });
      return;
    }
    setBlocked(null);
    refused.current = false;
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
    <Panel as="fieldset" title={t("update.reason")}>
      <Field label={t("update.reason")} htmlFor="justification" hint={t("update.reasonHint")}>
        <Textarea
          id="justification"
          rows={2}
          placeholder={t("update.reasonPlaceholder")}
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
          <p className="rounded-lg border border-line bg-sunken px-4 py-3 text-secondary">{t("update.secrets")}</p>
        )}
        <FormRenderer
          form={form}
          values={values}
          onChange={setValue}
          onBlur={(name) => setTouched((was) => (was.has(name) ? was : new Set(was).add(name)))}
          errors={fieldErrors}
          ctx={{ versionId: form.version_id, projectId: d.project.id, environment: d.environment }}
          fieldStates={p?.fields}
          ref={formRef}
          leading={
            item?.versions && (
              <VersionPicker
                item={item}
                d={d}
                value={form.version_id}
                onChange={(id) => onVersion(id, kept())}
                error={fieldErrors.$version_id}
              />
            )
          }
          leadingFields={["$version_id"]}
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
                {t("update.review")}
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
          returnFocus={() => (refused.current ? (focusedNow() ?? attentionRef.current) : null)}
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
  returnFocus,
}: {
  name: string;
  preview: UpdatePreview;
  /** Counts the refusals that only warned: each moves the focus to the warnings. */
  refused?: number;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  returnFocus?: () => HTMLElement | null | undefined;
}) {
  const { t } = useTranslation("deployments");
  const destructive = p.changes.filter((c) => c.destructive);
  const rest = p.changes.length - destructive.length;
  const warned = Boolean(p.warnings?.length);
  const confirmLabel = p.approval.required
    ? t(warned ? "update.confirm.submitAnyway" : "update.confirm.submit")
    : t(warned ? "update.confirm.updateAnyway" : "update.confirm.update");
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && onCancel()}
      title={t("update.confirm.title", { name })}
      description={
        p.changes.length === 0
          ? t("update.nothingChanges")
          : t(p.approval.required ? "update.confirm.approval" : "update.confirm.now", { count: p.changes.length })
      }
      consequences={
        (p.changes.length > 0 || Boolean(p.warnings?.length)) && (
          <div className="flex flex-col gap-2">
            {p.changes.length > 0 && (
              <ul className="flex list-disc flex-col gap-0.5 pl-4">
                {(destructive.length ? destructive : p.changes).map((c, i) => (
                  <li key={`${c.kind}-${c.vm ?? ""}-${i}`}>
                    {c.destructive && c.detail
                      ? t("update.changeDetail", {
                          title: c.title,
                          detail: `${c.detail.charAt(0).toLowerCase()}${c.detail.slice(1)}`,
                        })
                      : c.title}
                  </li>
                ))}
                {destructive.length > 0 && rest > 0 && <li>{t("update.more", { count: rest })}</li>}
              </ul>
            )}
            <PolicyWarnings warnings={p.warnings ?? []} note="" refusal={refused} />
          </div>
        )
      }
      confirmLabel={confirmLabel}
      cancelLabel={t("update.confirm.keepEditing")}
      tone={destructive.length ? "danger" : "primary"}
      pending={pending}
      onConfirm={onConfirm}
      returnFocus={returnFocus}
    />
  );
}

/** What has the focus, unless it fell to the page. */
function focusedNow(): HTMLElement | null {
  const el = document.activeElement;
  return el instanceof HTMLElement && el !== document.body ? el : null;
}

export function UpdatePage({ deploymentId }: { deploymentId: string }) {
  const { t } = useTranslation("deployments");
  const dep = useQuery(q.deployment(deploymentId));
  const d = dep.data;
  const slug = d?.catalog_item.slug ?? "";
  const projectId = d?.project.id ?? "";
  const item = useQuery({ ...q.catalogItem(slug, projectId), enabled: Boolean(d) });
  const [picked, setPicked] = useState<{ id: string; start: UpdateStart }>();
  const focusVersion = useRef(false);
  const form = useQuery({ ...q.form(slug, projectId, pickedVersion(item.data, picked?.id)), enabled: Boolean(d) });
  const error = dep.error ?? item.error ?? form.error;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/deployments">{t("list.title")}</Link>
            <Link to="/deployments/$deploymentId" params={{ deploymentId }}>
              {d?.name ?? "…"}
            </Link>
          </Breadcrumbs>
        }
        title={d ? t("update.title", { name: d.name }) : <Skeleton className="h-5 w-56" />}
        description={t("update.lead")}
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
        <UpdateBody
          key={form.data.version_id}
          d={d}
          form={form.data}
          item={item.data}
          start={picked?.start}
          onVersion={(id, keep) => {
            // A refusal moves the focus to what it is about instead.
            focusVersion.current = !keep.refusal;
            setPicked({ id, start: keep });
          }}
          focusVersion={focusVersion}
        />
      )}
    </Page>
  );
}

/** The version to update to, when the item offers several (ADR-0046): the newest unless another is picked. */
function VersionPicker({
  item,
  d,
  value,
  onChange,
  error,
}: {
  item: CatalogItem;
  d: DeploymentDetail;
  value: string;
  onChange: (versionId: string) => void;
  error?: string;
}) {
  const { t } = useTranslation("deployments");
  const label = (v: { id: string; number: string }) =>
    v.id === item.version.id
      ? t("update.version.newest", { number: v.number })
      : v.id === d.version.id
        ? t("update.version.own", { number: v.number })
        : v.number;
  return (
    <Field
      label={t("details.version")}
      htmlFor="version"
      error={error}
      hint={value === d.version.id ? t("update.version.ownHint") : t("update.version.moveHint")}
    >
      <Select
        id="version"
        value={value}
        onChange={onChange}
        options={(item.versions ?? []).map((v) => ({ value: v.id, label: label(v) }))}
      />
    </Field>
  );
}

function UpdateMeta({ d, item }: { d: DeploymentDetail; item?: CatalogItem }) {
  const { t } = useTranslation("deployments");
  return (
    <>
      <span className="flex items-center gap-1.5">
        {d.project.name} <EnvBadge env={d.environment} />
      </span>
      <span>
        {d.catalog_item.name} {d.version.number}
        {item && item.version.id !== d.version.id
          ? ` · ${t("update.version.out", { number: item.version.number })}`
          : ""}
      </span>
    </>
  );
}
