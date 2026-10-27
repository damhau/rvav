import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
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
} from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, fullDate, newIdempotencyKey, roughDuration } from "@/lib/utils";

const quotaLabel: Record<QuotaResource, [string, string]> = {
  vcpu: ["vCPU", ""],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs", ""],
};

type ApprovalPreview = RequestPreview["approval"];

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
}: {
  preview: RequestPreview | undefined;
  fetching: boolean;
  values: Record<string, unknown>;
  pending: boolean;
  formErrors: string[];
  onSubmit: () => void;
}) {
  const approval = preview?.approval;
  const post = Array.isArray(values.post_config) ? (values.post_config as string[]) : [];
  const rows = [
    preview?.names?.length
      ? { label: preview.names.length > 1 ? "Names" : "Name", value: preview.names.join(", "), mono: true }
      : null,
    preview?.placement?.cluster
      ? {
          label: "Placement",
          value: [preview.placement.cluster, preview.placement.datastore].filter(Boolean).join(" · "),
          mono: true,
        }
      : null,
    preview?.placement?.network
      ? {
          label: "Network",
          value: [preview.placement.network, preview.placement.subnet].filter(Boolean).join(" · "),
          mono: true,
        }
      : null,
    preview?.addresses?.length ? { label: "Address", value: `${preview.addresses[0]} (next free)`, mono: true } : null,
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
                {approval?.required ? approvalBody(approval) : "Provisioning starts as soon as you submit."}
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
}: {
  item: CatalogItem;
  form: RequestForm;
  project: Project;
  environments: Environment[];
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const firstEnv =
    environments
      .filter((e) => project.environments.includes(e.key) && !e.production)
      .sort((a, b) => a.order - b.order)[0]?.key ??
    project.environments[0] ??
    "dev";
  const [environment, setEnvironment] = useState(firstEnv);
  const [values, setValues] = useState<Record<string, unknown>>(() => ({ ...form.initial_values }));
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [submitted, setSubmitted] = useState(false);
  const [serverErrors, setServerErrors] = useState<MappedErrors | null>(null);
  const [justification, setJustification] = useState("");
  const [leaseDays, setLeaseDays] = useState<number | null>(null);
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey);

  const draft = useMemo<RequestDraft>(
    () => ({
      catalog_item_id: item.id,
      version_id: form.version_id,
      project_id: project.id,
      environment,
      inputs: values,
      count: 1,
      ...(justification ? { justification } : {}),
      lease_days: leaseDays,
    }),
    [item.id, form.version_id, project.id, environment, values, justification, leaseDays],
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
        />
      </aside>
    </div>
  );
}

export function RequestFormPage({ itemKey }: { itemKey: string }) {
  const { projectId } = useCurrentProject();
  const item = useQuery(q.catalogItem(itemKey, projectId));
  const form = useQuery(q.form(itemKey, projectId));
  const project = useQuery(q.project(projectId));
  const environments = useQuery(q.environments());
  const error = item.error ?? form.error ?? project.error ?? environments.error;

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
      ) : !item.data || !form.data || !project.data || !environments.data ? (
        <div className="flex flex-wrap gap-4">
          <div className="flex flex-[999_1_560px] flex-col gap-4">
            <Skeleton className="h-40 rounded-lg" />
            <Skeleton className="h-32 rounded-lg" />
          </div>
          <Skeleton className="h-64 flex-[1_1_320px] rounded-lg" />
        </div>
      ) : (
        <RequestFormBody
          key={`${form.data.version_id}-${projectId}`}
          item={item.data}
          form={form.data}
          project={project.data}
          environments={environments.data.items}
        />
      )}
    </Page>
  );
}
