import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { UserPicker } from "@/features/admin/governance/common";
import { FormRenderer } from "@/forms/FormRenderer";
import { mapFieldErrors, type MappedErrors } from "@/forms/errors";
import { api, call, isApiError } from "@/lib/api/client";
import type {
  CatalogItem,
  Environment,
  Project,
  QuotaResource,
  RequestDraft,
  RequestForm,
  RequestPreview,
  UserRef,
} from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, fullDate, newIdempotencyKey, roughDuration } from "@/lib/utils";
import { ComponentsPreview } from "./ComponentsPreview";

const quotaLabel: Record<QuotaResource, [string, string]> = {
  vcpu: ["vCPU", ""],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs", ""],
};

type ApprovalPreview = RequestPreview["approval"];

/** Where a form starts from: a saved draft, or a past request asked again. */
export interface FormStart {
  environment?: string;
  inputs?: Record<string, unknown>;
  justification?: string;
  leaseDays?: number | null;
  count?: number;
  scheduledAt?: string | null;
  onBehalfOf?: UserRef;
}

/** A date-time for a datetime-local input, in local time. */
function localInput(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "Needs approval from Infrastructure leads", or "…, then Network security" for several levels. */
function approvalTitle(approval: ApprovalPreview) {
  const groups = [...new Set(approval.policies.flatMap((p) => p.levels.map((l) => l.name ?? "an approver")))];
  return `Needs approval from ${groups.join(", then ")}`;
}

/** The policy reasons as written by admins, plus the usual wait. */
function approvalBody(approval: ApprovalPreview) {
  const reasons = approval.policies.map((p) => p.reason.replace(/\.$/, "")).join(". ");
  const wait = approval.typical_wait_seconds;
  if (!wait) return `${reasons}.`;
  const usual = wait < 5400 ? `${Math.max(1, Math.round(wait / 60))} minutes` : `${Math.round(wait / 3600)} hours`;
  return `${reasons}. Usually decided within ${usual}.`;
}

function EnvironmentPicker({
  environments,
  allowed,
  value,
  onChange,
  error,
}: {
  environments: Environment[];
  allowed: string[];
  value: string;
  onChange: (env: string) => void;
  error?: string;
}) {
  const list = environments.filter((e) => allowed.includes(e.key)).sort((a, b) => a.order - b.order);
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span id="env-label" className="text-xs font-medium">
        Environment
      </span>
      <div
        role="radiogroup"
        aria-labelledby="env-label"
        className="flex overflow-hidden rounded-md border border-line-strong"
      >
        {list.map((e) => {
          const active = e.key === value;
          return (
            <button
              key={e.key}
              type="button"
              role="radio"
              aria-checked={active}
              title={e.name}
              onClick={() => onChange(e.key)}
              className={cn(
                "h-8 flex-1 border-l border-line-strong font-mono text-[12px] uppercase first:border-l-0",
                active ? "bg-fg font-medium text-bg" : "bg-surface text-muted hover:bg-sunken",
              )}
            >
              {e.key}
            </button>
          );
        })}
      </div>
      {error ? (
        <p className="text-xs text-bad">{error}</p>
      ) : (
        <p className="text-[11px] text-faint">
          {list.find((e) => e.key === value)?.production
            ? "Production: stricter policies and no lease."
            : "Placement, quota and policies follow the environment."}
        </p>
      )}
    </div>
  );
}

function Summary({
  preview,
  fetching,
  values,
  pending,
  formErrors,
  onSubmit,
  onSaveDraft,
  savingDraft,
  scheduled,
  oneDeployment,
}: {
  preview: RequestPreview | undefined;
  fetching: boolean;
  values: Record<string, unknown>;
  pending: boolean;
  formErrors: string[];
  onSubmit: () => void;
  onSaveDraft: () => void;
  savingDraft: boolean;
  scheduled: boolean;
  /** A multi-VM item: what each component builds replaces the single VM's lines. */
  oneDeployment: boolean;
}) {
  const approval = preview?.approval;
  const post = Array.isArray(values.post_config) && !oneDeployment ? (values.post_config as string[]) : [];
  const single = !oneDeployment;
  const rows = [
    single && preview?.names?.length
      ? { label: preview.names.length > 1 ? "Names" : "Name", value: preview.names.join(", "), mono: true }
      : null,
    single && preview?.placement?.cluster
      ? {
          label: "Placement",
          value: [preview.placement.cluster, preview.placement.datastore].filter(Boolean).join(" · "),
          mono: true,
        }
      : null,
    single && preview?.placement?.network
      ? {
          label: "Network",
          value: [preview.placement.network, preview.placement.subnet].filter(Boolean).join(" · "),
          mono: true,
        }
      : null,
    single && preview?.addresses?.length
      ? { label: "Address", value: `${preview.addresses[0]} (next free)`, mono: true }
      : null,
    post.length
      ? { label: "After boot", value: `${post.length} AWX job${post.length === 1 ? "" : "s"}: ${post.join(", ")}` }
      : null,
    preview?.lease
      ? {
          label: "Lease",
          value: preview.lease.expires_at ? `Until ${fullDate(preview.lease.expires_at)}` : "No expiry",
        }
      : null,
  ].filter((r): r is { label: string; value: string; mono?: boolean } => r !== null);

  return (
    <div className="flex flex-col gap-3 lg:sticky lg:top-4">
      <section className="rounded-lg border border-line bg-surface" aria-label="Summary" aria-busy={fetching}>
        <div className="flex items-center justify-between border-b border-line px-4 py-2.5">
          <h2 className="text-[13px] font-semibold">Summary</h2>
          {fetching && <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label="Updating" />}
        </div>
        {!preview ? (
          <div className="flex flex-col gap-2 p-4">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        ) : (
          <>
            {rows.length > 0 && (
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5 px-4 py-3">
                {rows.map((r) => (
                  <div key={r.label} className="contents">
                    <dt className="text-muted">{r.label}</dt>
                    <dd className={cn("m-0 break-words", r.mono && "font-mono text-[12px]")}>{r.value}</dd>
                  </div>
                ))}
              </dl>
            )}
            {oneDeployment && preview.components && <ComponentsPreview components={preview.components} />}
            {preview.quota.length > 0 && (
              <div className="flex flex-col gap-2.5 border-t border-line px-4 py-3">
                <span className="label">Quota after this request</span>
                {preview.quota.map((l) => (
                  <Meter
                    key={`${l.resource}-${l.environment ?? ""}`}
                    label={`${quotaLabel[l.resource][0]}${l.environment ? ` in ${l.environment}` : ""}${
                      l.exceeds && l.enforcement === "soft" ? " · over the soft limit" : ""
                    }`}
                    unit={quotaLabel[l.resource][1]}
                    used={l.used}
                    adding={l.adding}
                    total={l.limit ?? l.used + l.adding}
                  />
                ))}
              </div>
            )}
            <div
              className={cn(
                "flex flex-col gap-0.5 rounded-b-lg border-t border-line px-4 py-3",
                approval?.required ? "bg-warn-soft text-warn" : "bg-ok-soft text-ok",
              )}
            >
              <span className="font-medium">{approval?.required ? approvalTitle(approval) : "No approval needed"}</span>
              <span className="text-xs">
                {approval?.required
                  ? approvalBody(approval)
                  : scheduled
                    ? "Provisioning starts at the time you chose."
                    : "Provisioning starts as soon as you submit."}
              </span>
            </div>
          </>
        )}
      </section>
      {formErrors.length > 0 && (
        <ul
          role="alert"
          className="flex flex-col gap-1 rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-xs text-bad"
        >
          {formErrors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
      <Button variant="primary" className="h-9" type="button" onClick={onSubmit} disabled={pending}>
        {pending && <LoaderCircle className="animate-spin" aria-hidden />}
        {approval?.required ? "Submit for approval" : "Submit request"}
      </Button>
      <Button type="button" onClick={onSaveDraft} disabled={savingDraft}>
        Save as draft
      </Button>
      <p className="text-center text-[11px] text-faint">
        You can follow every step live, including the AWX job output.
      </p>
    </div>
  );
}

function RequestFormBody({
  item,
  form,
  project,
  environments,
  start,
  draftId,
  canActForOthers,
}: {
  item: CatalogItem;
  form: RequestForm;
  project: Project;
  environments: Environment[];
  start?: FormStart;
  draftId?: string;
  canActForOthers: boolean;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const firstEnv =
    environments
      .filter((e) => project.environments.includes(e.key) && !e.production)
      .sort((a, b) => a.order - b.order)[0]?.key ??
    project.environments[0] ??
    "dev";
  const [environment, setEnvironment] = useState(
    start?.environment && project.environments.includes(start.environment) ? start.environment : firstEnv,
  );
  const [values, setValues] = useState<Record<string, unknown>>(() => {
    // What the form has now: fields since removed, and masked secrets, stay out.
    const props = (form.schema as { properties?: Record<string, unknown> }).properties ?? {};
    const kept = Object.entries(start?.inputs ?? {}).filter(([k, v]) => k in props && v !== "••••");
    return { ...form.initial_values, ...Object.fromEntries(kept) };
  });
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [submitted, setSubmitted] = useState(false);
  const [serverErrors, setServerErrors] = useState<MappedErrors | null>(null);
  const [justification, setJustification] = useState(start?.justification ?? "");
  const [leaseDays, setLeaseDays] = useState<number | null>(start?.leaseDays ?? null);
  const [count, setCount] = useState(String(start?.count ?? 1));
  const [later, setLater] = useState(Boolean(start?.scheduledAt));
  const [startAt, setStartAt] = useState(localInput(start?.scheduledAt));
  const { me } = useCurrentProject();
  // Only project admins request for others; for oneself is not "on behalf".
  const [onBehalfOf, setOnBehalfOf] = useState<UserRef | null>(
    canActForOthers && start?.onBehalfOf && start.onBehalfOf.id !== me.user.id ? start.onBehalfOf : null,
  );
  const [savedDraft, setSavedDraft] = useState(draftId);
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey);
  // Several deployments of a single-VM item; a multi-VM item's counts are in its form.
  const many = item.type === "vm_blueprint" && !form.one_deployment;

  const draft = useMemo<RequestDraft>(
    () => ({
      catalog_item_id: item.id,
      version_id: form.version_id,
      project_id: project.id,
      environment,
      inputs: values,
      count: many ? Math.max(1, Number(count) || 1) : 1,
      ...(justification ? { justification } : {}),
      lease_days: leaseDays,
      ...(later && startAt ? { scheduled_at: new Date(startAt).toISOString() } : {}),
      ...(onBehalfOf && canActForOthers ? { on_behalf_of: onBehalfOf.id } : {}),
    }),
    [
      canActForOthers,
      item.id,
      form.version_id,
      project.id,
      environment,
      values,
      justification,
      leaseDays,
      many,
      count,
      later,
      startAt,
      onBehalfOf,
    ],
  );
  const debounced = useDebounced(draft, 350);
  const preview = useQuery({
    queryKey: keys.preview(debounced),
    queryFn: ({ signal }) => call(api.POST("/requests/preview", { body: debounced, signal })),
    placeholderData: keepPreviousData,
  });

  const submit = useMutation({
    mutationFn: (body: RequestDraft) =>
      call(api.POST("/requests", { params: { header: { "Idempotency-Key": idempotencyKey } }, body })),
    onSuccess: (request) => {
      setIdempotencyKey(newIdempotencyKey());
      if (savedDraft) {
        // Submitted: the draft has done its job.
        // Already discarded elsewhere is fine: it is gone either way.
        call(api.DELETE("/me/drafts/{id}", { params: { path: { id: savedDraft } } }))
          .catch(() => undefined)
          .finally(() => void qc.invalidateQueries({ queryKey: ["me", "drafts"] }));
      }
      void qc.invalidateQueries({ queryKey: keys.requests.lists() });
      void qc.invalidateQueries({ queryKey: keys.deployments.lists() });
      toast.success(`${request.number} submitted`, {
        description: request.status === "pending_approval" ? "Waiting for approval." : "Provisioning has started.",
      });
      void navigate({ to: "/requests/$requestId", params: { requestId: request.number } });
    },
    onError: (e) => {
      setSubmitted(true);
      if (isApiError(e) && e.status === 422) {
        setServerErrors(mapFieldErrors(e.fieldErrors));
        toast.error(e.problem.title, { description: e.problem.detail });
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : "The request was not sent", {
        description: isApiError(e) ? `${e.problem.detail ?? ""} Request ID ${e.requestId}`.trim() : undefined,
      });
    },
  });

  const saveDraft = useMutation({
    mutationFn: () =>
      savedDraft
        ? call(api.PUT("/me/drafts/{id}", { params: { path: { id: savedDraft } }, body: { draft } }))
        : call(api.POST("/me/drafts", { body: { draft } })),
    onSuccess: (saved) => {
      setSavedDraft(saved.id);
      void qc.invalidateQueries({ queryKey: ["me", "drafts"] });
      toast.success("Draft saved", { description: "Find it under Requests, to finish later." });
    },
    onError: (e) => {
      toast.error(isApiError(e) ? e.problem.title : "The draft was not saved", {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
    },
  });

  const previewErrors = mapFieldErrors(preview.data?.errors);
  const fieldErrors: Record<string, string> = {};
  for (const [name, msg] of Object.entries(previewErrors.fields))
    if (submitted || touched.has(name)) fieldErrors[name] = msg;
  Object.assign(fieldErrors, serverErrors?.fields ?? {});
  const formErrors = submitted ? [...new Set([...(serverErrors?.form ?? []), ...previewErrors.form])] : [];

  const setValue = (name: string, value: unknown) => {
    setValues((v) => (v[name] === value ? v : { ...v, [name]: value }));
    if (serverErrors?.fields[name]) setServerErrors((s) => s && { ...s, fields: { ...s.fields, [name]: "" } });
  };

  const lease = preview.data?.lease;
  const leaseOptions =
    lease && lease.max_days !== null && lease.max_days !== undefined
      ? [7, 14, 30, 60, 90].filter((d) => d <= (lease.max_days ?? 90))
      : [];

  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
        <FormRenderer
          form={form}
          values={values}
          onChange={setValue}
          onBlur={(name) => setTouched((t) => (t.has(name) ? t : new Set(t).add(name)))}
          errors={Object.fromEntries(Object.entries(fieldErrors).filter(([, m]) => m))}
          ctx={{ versionId: form.version_id, projectId: project.id, environment }}
          fieldStates={preview.data?.fields}
          leading={
            <EnvironmentPicker
              environments={environments}
              allowed={project.environments}
              value={environment}
              onChange={(e) => {
                setEnvironment(e);
                setLeaseDays(null);
              }}
              error={fieldErrors.$environment}
            />
          }
        />
        <fieldset className="m-0 min-w-0 rounded-lg border border-line bg-surface p-0">
          <legend className="sr-only">Lease and reason</legend>
          <div className="border-b border-line px-4 py-2.5 text-[13px] font-semibold" aria-hidden>
            Lease and reason
          </div>
          <div className="grid grid-cols-1 gap-4 p-4 md:grid-cols-2">
            <div className="flex min-w-0 flex-col gap-1.5">
              <label htmlFor="lease" className="text-xs font-medium">
                Lease
              </label>
              {leaseOptions.length ? (
                <Select
                  id="lease"
                  value={String(leaseDays ?? lease?.default_days ?? 30)}
                  onChange={(v) => setLeaseDays(Number(v))}
                  options={leaseOptions.map((d) => ({ value: String(d), label: `${d} days` }))}
                />
              ) : (
                <span
                  id="lease"
                  className="flex h-8 items-center rounded-md border border-dashed border-line-strong bg-sunken px-2.5 text-muted"
                >
                  No expiry
                </span>
              )}
              <p className="text-[11px] text-faint">
                {leaseOptions.length
                  ? "You get reminders 7 days and 1 day before. You can extend it twice."
                  : "Production deployments and AWX jobs have no lease."}
              </p>
            </div>
            <div className="flex min-w-0 flex-col gap-1.5">
              <label htmlFor="justification" className="text-xs font-medium">
                Reason <span className="font-normal text-faint">· shown to approvers</span>
              </label>
              <Textarea
                id="justification"
                rows={2}
                placeholder="What is this for? Add a ticket number if there is one."
                value={justification}
                onChange={(e) => setJustification(e.target.value)}
              />
            </div>
          </div>
        </fieldset>
        <fieldset className="m-0 min-w-0 rounded-lg border border-line bg-surface p-0">
          <legend className="sr-only">Options</legend>
          <div className="border-b border-line px-4 py-2.5 text-[13px] font-semibold" aria-hidden>
            Options
          </div>
          <div className="grid grid-cols-1 gap-4 p-4 md:grid-cols-2">
            {many && (
              <div className="flex min-w-0 flex-col gap-1.5">
                <label htmlFor="count" className="text-xs font-medium">
                  How many
                </label>
                <Input
                  id="count"
                  type="number"
                  min={1}
                  max={20}
                  className="w-24"
                  value={count}
                  onChange={(e) => setCount(e.target.value)}
                  aria-invalid={Boolean(fieldErrors.$count) || undefined}
                />
                <p className="text-[11px] text-faint">
                  {fieldErrors.$count ?? "Identical deployments, named one after the other (at most 20)."}
                </p>
              </div>
            )}
            <div className="flex min-w-0 flex-col gap-1.5">
              <label htmlFor="start" className="text-xs font-medium">
                Start
              </label>
              <Select
                id="start"
                value={later ? "later" : "now"}
                onChange={(v) => setLater(v === "later")}
                options={[
                  { value: "now", label: "As soon as it is approved" },
                  { value: "later", label: "At a time I choose" },
                ]}
              />
              {later && (
                <Input
                  id="start-at"
                  type="datetime-local"
                  aria-label="Start at"
                  value={startAt}
                  onChange={(e) => setStartAt(e.target.value)}
                  aria-invalid={Boolean(fieldErrors.$scheduled_at) || undefined}
                />
              )}
              {fieldErrors.$scheduled_at && <p className="text-[11px] text-bad">{fieldErrors.$scheduled_at}</p>}
            </div>
            {canActForOthers && (
              <div className="flex min-w-0 flex-col gap-1.5 md:col-span-2">
                {onBehalfOf ? (
                  <>
                    <span className="text-xs font-medium">For someone else</span>
                    <div className="flex items-center gap-2 text-[13px]">
                      <span>
                        On behalf of <strong>{onBehalfOf.name}</strong>{" "}
                        <span className="text-xs text-faint">{onBehalfOf.email}</span>: they will own it.
                      </span>
                      <Button size="sm" variant="ghost" onClick={() => setOnBehalfOf(null)}>
                        For myself instead
                      </Button>
                    </div>
                  </>
                ) : (
                  <UserPicker label="For someone else (optional)" id="on-behalf" onPick={setOnBehalfOf} />
                )}
                {fieldErrors.$on_behalf_of && <p className="text-[11px] text-bad">{fieldErrors.$on_behalf_of}</p>}
              </div>
            )}
          </div>
        </fieldset>
      </div>
      <aside className="flex min-w-0 flex-[1_1_320px] flex-col self-stretch">
        <Summary
          preview={preview.data}
          fetching={preview.isFetching}
          values={values}
          pending={submit.isPending}
          formErrors={formErrors}
          onSubmit={() => {
            setSubmitted(true);
            submit.mutate(draft);
          }}
          onSaveDraft={() => saveDraft.mutate()}
          savingDraft={saveDraft.isPending}
          scheduled={later && Boolean(startAt)}
          oneDeployment={Boolean(form.one_deployment)}
        />
      </aside>
    </div>
  );
}

export function RequestFormPage({ itemKey, draftId, from }: { itemKey: string; draftId?: string; from?: string }) {
  const { me, projectId, role, setProjectId } = useCurrentProject();
  const item = useQuery(q.catalogItem(itemKey, projectId));
  const form = useQuery(q.form(itemKey, projectId));
  const project = useQuery(q.project(projectId));
  const environments = useQuery(q.environments());
  const saved = useQuery({ ...q.draft(draftId ?? ""), enabled: Boolean(draftId) });
  const past = useQuery({ ...q.request(from ?? ""), enabled: Boolean(from) });
  // A draft or a past request is resumed in its own project: switched to
  // once, when the caller works there.
  const startProject = saved.data?.project.id ?? past.data?.project.id;
  const member = startProject === undefined || me.projects.some((p) => p.project.id === startProject);
  const switched = useRef(false);
  useEffect(() => {
    if (!startProject || !member || switched.current) return;
    switched.current = true;
    if (startProject !== projectId) setProjectId(startProject);
  }, [startProject, member, projectId, setProjectId]);
  const start: FormStart | undefined = saved.data
    ? {
        environment: saved.data.draft.environment,
        inputs: saved.data.draft.inputs,
        justification: saved.data.draft.justification,
        leaseDays: saved.data.draft.lease_days,
        count: saved.data.draft.count,
        scheduledAt: saved.data.draft.scheduled_at,
        onBehalfOf: saved.data.on_behalf_of,
      }
    : past.data
      ? {
          environment: past.data.environment,
          inputs: past.data.inputs,
          justification: past.data.justification,
          count: past.data.count,
          onBehalfOf: past.data.on_behalf_of,
        }
      : undefined;
  const waiting =
    (Boolean(draftId) && !saved.data) ||
    (Boolean(from) && !past.data) ||
    (member && startProject !== undefined && startProject !== projectId && !switched.current);
  const elsewhere = member
    ? null
    : new Error("This is in a project you don't work in: ask one of its members to request it.");
  const error =
    item.error ?? form.error ?? project.error ?? environments.error ?? saved.error ?? past.error ?? elsewhere;

  return (
    <Page>
      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-faint">
          <Link to="/catalog">Catalog</Link> / {item.data?.name ?? "…"}
        </span>
        <div className="flex items-center gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface font-mono text-[11px] font-medium text-muted">
            {item.data?.type === "vm_blueprint" ? "VM" : item.data ? "AWX" : ""}
          </span>
          <div className="flex min-w-0 flex-col">
            <h1 className="text-lg font-semibold tracking-[-0.01em]">
              {item.data ? `Request ${item.data.name}` : <Skeleton className="h-5 w-56" />}
            </h1>
            {item.data && (
              <span className="text-xs text-muted">
                {itemTypeLabel[item.data.type]} {item.data.version.number}
                {item.data.source.awx_template_name ? ` · launches ${item.data.source.awx_template_name}` : ""}
                {item.data.typical_duration_seconds
                  ? ` · usually done in ${roughDuration(item.data.typical_duration_seconds)}`
                  : ""}
              </span>
            )}
          </div>
        </div>
      </div>
      {error ? (
        <ErrorState error={error} className="rounded-lg border border-line bg-surface" />
      ) : !item.data || !form.data || !project.data || !environments.data || waiting ? (
        <div className="flex flex-wrap gap-4">
          <div className="flex flex-[999_1_560px] flex-col gap-4">
            <Skeleton className="h-40 rounded-lg" />
            <Skeleton className="h-32 rounded-lg" />
          </div>
          <Skeleton className="h-64 flex-[1_1_320px] rounded-lg" />
        </div>
      ) : (
        <RequestFormBody
          key={`${form.data.version_id}-${projectId}-${draftId ?? from ?? ""}`}
          item={item.data}
          form={form.data}
          project={project.data}
          environments={environments.data.items}
          start={start}
          draftId={draftId}
          canActForOthers={role === "project-admin"}
        />
      )}
    </Page>
  );
}
