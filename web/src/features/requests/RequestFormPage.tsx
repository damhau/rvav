import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
// The graph's model only: the canvas itself (React Flow) loads with RequestTopology.
import { blueprintGraph } from "@/components/canvas/blueprint";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { Field, FieldNote, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { UserPicker } from "@/features/admin/governance/common";
import { FormRenderer, shownFields, type FormHandle, type PageInputs } from "@/forms/FormRenderer";
import { Attention, fieldControl, type Unplaced } from "@/forms/attention";
import { sentInputs, withDefaults } from "@/forms/defaults";
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
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, fullDate, localInput, newIdempotencyKey, roughDuration } from "@/lib/utils";
import { ComponentsPreview, PlacedBy } from "./ComponentsPreview";

const RequestTopology = lazy(() => import("./RequestTopology"));

export const quotaLabel: Record<QuotaResource, [string, string]> = {
  vcpu: ["vCPU", ""],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs", ""],
};

type ApprovalPreview = RequestPreview["approval"];

/** The request's own options, by the key their errors map to (`mapFieldErrors`). */
const requestOptions: Record<string, string> = {
  $environment: "Environment",
  $count: "How many",
  $scheduled_at: "Start at",
  $on_behalf_of: "For someone else",
};

/** The control that fixes an input's problem. */
function controlOf(name: string): HTMLElement | null {
  switch (name) {
    case "$environment":
      return document.querySelector('[aria-labelledby="env-label"] [aria-checked="true"]');
    case "$count":
      return document.getElementById("count");
    case "$scheduled_at":
      return document.getElementById("start-at");
    case "$on_behalf_of":
      return document.getElementById("on-behalf");
    default:
      return fieldControl(name);
  }
}

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

/** "Needs approval from Infrastructure leads", or "…, then Network security" for several levels. */
export function approvalTitle(approval: ApprovalPreview) {
  const groups = [...new Set(approval.policies.flatMap((p) => p.levels.map((l) => l.name ?? "an approver")))];
  return `Needs approval from ${groups.join(", then ")}`;
}

/** The policy reasons as written by admins, plus the usual wait. */
export function approvalBody(approval: ApprovalPreview) {
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
      <span id="env-label" className="text-secondary font-medium">
        Environment
      </span>
      <div
        role="radiogroup"
        aria-labelledby="env-label"
        aria-describedby="env-note"
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
                "h-8 flex-1 border-l border-line-strong font-mono text-secondary uppercase first:border-l-0",
                active ? "bg-fg font-medium text-bg" : "bg-surface text-muted hover:bg-sunken",
              )}
            >
              {e.key}
            </button>
          );
        })}
      </div>
      <FieldNote
        id="env-note"
        error={error}
        hint={
          list.find((e) => e.key === value)?.production
            ? "Production: stricter policies and no lease."
            : "Placement, quota and policies follow the environment."
        }
      />
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
  composite,
  previewError,
  onRetryPreview,
}: {
  preview: RequestPreview | undefined;
  /** Why the summary couldn't be worked out, when it couldn't. */
  previewError: unknown;
  onRetryPreview: () => void;
  fetching: boolean;
  values: Record<string, unknown>;
  pending: boolean;
  formErrors: string[];
  /** Unset when the form's own last step submits (a wizard's review). */
  onSubmit?: () => void;
  onSaveDraft: () => void;
  savingDraft: boolean;
  scheduled: boolean;
  /** A multi-VM item: what each component builds replaces the single VM's lines. */
  oneDeployment: boolean;
  /** A composite item: what each of its VM steps builds. */
  composite: boolean;
}) {
  const approval = preview?.approval;
  const post = Array.isArray(values.post_config) && !oneDeployment ? (values.post_config as string[]) : [];
  const single = !oneDeployment;
  const placement = single ? preview?.components?.[0]?.placement : undefined;
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
    single && (placement?.resource_pool || placement?.hosts?.length || placement?.datastore_cluster)
      ? {
          label: "Within",
          value: [
            placement.resource_pool && `pool ${placement.resource_pool}`,
            placement.hosts?.length && `hosts ${placement.hosts.join(", ")}`,
            placement.datastore_cluster && `datastore cluster ${placement.datastore_cluster}`,
          ]
            .filter(Boolean)
            .join(" · "),
          mono: true,
        }
      : null,
    single && placement?.strategy && placement.strategy !== "most_free_memory"
      ? {
          label: "Chosen by",
          value: placement.strategy === "binpack" ? "Binpack" : "Spread",
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
    <div className="lg:sticky lg:top-4" aria-busy={fetching}>
      <Panel
        title="Summary"
        flush
        actions={fetching && <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label="Updating" />}
        footer={
          <>
            <Button type="button" onClick={onSaveDraft} disabled={savingDraft}>
              Save as draft
            </Button>
            {onSubmit && <SubmitButton onClick={onSubmit} pending={pending} approval={approval?.required} />}
          </>
        }
      >
        {!preview && previewError ? (
          <ErrorState error={previewError} onRetry={onRetryPreview} />
        ) : !preview ? (
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
                    <dd className={cn("m-0 break-words", r.mono && "font-mono text-secondary")}>{r.value}</dd>
                  </div>
                ))}
              </dl>
            )}
            {single &&
            !composite &&
            (preview.components?.[0]?.placement?.constraints?.length ||
              preview.components?.[0]?.placement?.image?.excluded.length) ? (
              <div className="flex flex-col gap-1.5 border-t border-line px-4 py-3">
                <span className="label">Placed by tags</span>
                <PlacedBy
                  constraints={preview.components[0].placement.constraints}
                  image={preview.components[0].placement.image}
                />
              </div>
            ) : null}
            {(oneDeployment || composite) && preview.components && (
              <ComponentsPreview components={preview.components} />
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
                    total={l.limit}
                  />
                ))}
              </div>
            )}
            <div
              className={cn(
                "flex flex-col gap-0.5 border-t border-line px-4 py-3",
                approval?.required ? "bg-warn-soft text-warn" : "bg-ok-soft text-ok",
              )}
            >
              <span className="font-medium">{approval?.required ? approvalTitle(approval) : "No approval needed"}</span>
              <span className="text-secondary">
                {approval?.required
                  ? approvalBody(approval)
                  : scheduled
                    ? "Provisioning starts at the time you chose."
                    : "Provisioning starts as soon as you submit."}
              </span>
            </div>
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

function SubmitButton({ onClick, pending, approval }: { onClick: () => void; pending: boolean; approval?: boolean }) {
  return (
    <Button variant="primary" type="button" onClick={onClick} disabled={pending}>
      {pending && <LoaderCircle className="animate-spin" aria-hidden />}
      {approval ? "Submit for approval" : "Submit request"}
    </Button>
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
  // Counts the server's refusals: each moves the focus to the first input to fix.
  const [refused, setRefused] = useState(0);
  const attentionRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<FormHandle>(null);
  // A wizard's page as Next checked it, and the debounced draft then: the check shows until the
  // preview of a later draft arrives, as an older one would hide what the check showed.
  const [checked, setChecked] = useState<{ since: RequestDraft; preview: RequestPreview }>();
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
      inputs: sentInputs(form.ui_schema.fields, values),
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
      form.ui_schema.fields,
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
  const shownPreview =
    checked && (debounced === checked.since || preview.isPlaceholderData) ? checked.preview : preview.data;

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
        const mapped = mapFieldErrors(e.fieldErrors);
        if (Object.keys(mapped.fields).length === 0 && mapped.form.length === 0)
          mapped.form.push(e.problem.detail ?? e.problem.title);
        setServerErrors(mapped);
        setRefused((n) => n + 1);
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

  const previewErrors = mapFieldErrors(shownPreview?.errors);
  const fieldErrors: Record<string, string> = {};
  for (const [name, msg] of Object.entries(previewErrors.fields))
    if (submitted || touched.has(name)) fieldErrors[name] = msg;
  // A server error goes once its input changes (cleared to "").
  for (const [name, msg] of Object.entries(serverErrors?.fields ?? {})) if (msg) fieldErrors[name] = msg;
  const formErrors = submitted ? [...new Set([...(serverErrors?.form ?? []), ...previewErrors.form])] : [];

  // The inputs the page has a control for, in its order: the rest are said at the top.
  const placed = [
    "$environment",
    ...shownFields(form, shownPreview?.fields),
    ...(many ? ["$count"] : []),
    ...(later ? ["$scheduled_at"] : []),
    ...(canActForOthers ? ["$on_behalf_of"] : []),
  ];
  const attention = submitted ? Object.keys(fieldErrors) : [];
  const unplaced: Unplaced[] = attention
    .filter((name) => !placed.includes(name))
    .map((name) => ({
      key: name,
      ...(requestOptions[name] ? { label: requestOptions[name] } : { label: name, code: true }),
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
    if (refused) focusFirstRef.current();
  }, [refused]);

  const clearServerError = (name: string) => {
    if (serverErrors?.fields[name]) setServerErrors((s) => s && { ...s, fields: { ...s.fields, [name]: "" } });
  };
  // A field the requester changed keeps a key, even emptied: it no longer takes its default.
  const setValue = (name: string, value: unknown) => {
    setValues((v) => (Object.hasOwn(v, name) && v[name] === value ? v : { ...v, [name]: value }));
    clearServerError(name);
  };
  // Next on a wizard's page: the preview of the form as it is now, its errors shown for the page's inputs.
  const checkPage = async (inputsOf: PageInputs) => {
    const body = draft;
    const since = debounced;
    const checkedPreview = await qc.fetchQuery({
      queryKey: keys.preview(body),
      queryFn: ({ signal }) => call(api.POST("/requests/preview", { body, signal })),
    });
    setChecked({ since, preview: checkedPreview });
    const inputs = inputsOf(checkedPreview.fields);
    setTouched((t) => new Set([...t, ...inputs]));
    const found = mapFieldErrors(checkedPreview.errors).fields;
    return { problems: inputs.filter((n) => found[n] || serverErrors?.fields[n]), fields: checkedPreview.fields };
  };
  // Before a wizard's review: the form's validations, which refuse as submit would.
  const checkAll = async () => {
    const checkedPreview = await call(
      api.POST("/requests/preview", { params: { query: { validate: true } }, body: draft }),
    );
    if (checkedPreview.errors.length === 0) {
      setServerErrors(null);
      return false;
    }
    const mapped = mapFieldErrors(checkedPreview.errors);
    setSubmitted(true);
    setServerErrors(mapped);
    const onInputs = Object.keys(mapped.fields).length > 0;
    if (onInputs) setRefused((n) => n + 1);
    return onInputs;
  };
  const submitDraft = () => {
    setSubmitted(true);
    submit.mutate(draft);
  };
  const stepped = form.ui_schema.layout.kind === "wizard";

  // What the item builds, drawn when there is more to it than one VM on a
  // network (the summary says that much): a composite's steps, an item's
  // components, segments and groups.
  const graph = useMemo(() => (form.topology ? blueprintGraph(form.topology, "/topology") : null), [form.topology]);
  const drawn = graph && (graph.kind === "composite" || graph.nodes.length > 2) ? graph : null;

  const lease = shownPreview?.lease;
  const leaseOptions =
    lease && lease.max_days !== null && lease.max_days !== undefined
      ? [7, 14, 30, 60, 90].filter((d) => d <= (lease.max_days ?? 90))
      : [];

  const requestPanels = (
    <>
      <Panel as="fieldset" title="Lease and reason">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <Field
            label="Lease"
            htmlFor="lease"
            hint={
              leaseOptions.length
                ? "You get reminders 7 days and 1 day before. You can extend it twice."
                : "Production deployments and AWX jobs have no lease."
            }
          >
            {leaseOptions.length ? (
              <Select
                id="lease"
                value={String(leaseDays ?? lease?.default_days ?? 30)}
                onChange={(v) => setLeaseDays(Number(v))}
                options={leaseOptions.map((d) => ({ value: String(d), label: `${d} days` }))}
              />
            ) : (
              <Input id="lease" readOnly value="No expiry" className="border-dashed bg-sunken text-muted" />
            )}
          </Field>
          <Field label="Reason" htmlFor="justification" hint="Approvers read it.">
            <Textarea
              id="justification"
              rows={2}
              placeholder="What is this for? Add a ticket number if there is one."
              value={justification}
              onChange={(e) => setJustification(e.target.value)}
            />
          </Field>
        </div>
      </Panel>
      <Panel as="fieldset" title="Options">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {many && (
            <Field
              label="How many"
              htmlFor="count"
              hint="Identical deployments, named one after the other (at most 20)."
              error={fieldErrors.$count}
            >
              <Input
                id="count"
                type="number"
                min={1}
                max={20}
                className="w-24"
                value={count}
                onChange={(e) => {
                  setCount(e.target.value);
                  clearServerError("$count");
                }}
              />
            </Field>
          )}
          <div className="flex min-w-0 flex-col gap-3">
            <Field label="Start" htmlFor="start">
              <Select
                id="start"
                value={later ? "later" : "now"}
                onChange={(v) => setLater(v === "later")}
                options={[
                  {
                    value: "now",
                    label: shownPreview?.approval.required ? "As soon as it is approved" : "As soon as it is submitted",
                  },
                  { value: "later", label: "At a time I choose" },
                ]}
              />
            </Field>
            {later && (
              <Field label="Start at" htmlFor="start-at" error={fieldErrors.$scheduled_at}>
                <Input
                  id="start-at"
                  type="datetime-local"
                  value={startAt}
                  onChange={(e) => {
                    setStartAt(e.target.value);
                    clearServerError("$scheduled_at");
                  }}
                />
              </Field>
            )}
          </div>
          {canActForOthers && (
            <div className="flex min-w-0 flex-col gap-1 md:col-span-2">
              {onBehalfOf ? (
                <>
                  <span className="text-secondary font-medium">For someone else</span>
                  <div className="flex items-center gap-2 text-body">
                    <span>
                      On behalf of <strong>{onBehalfOf.name}</strong>{" "}
                      <span className="text-secondary text-muted">{onBehalfOf.email}</span>: they will own it.
                    </span>
                    <Button
                      size="sm"
                      variant="quiet"
                      onClick={() => {
                        setOnBehalfOf(null);
                        clearServerError("$on_behalf_of");
                      }}
                    >
                      For myself instead
                    </Button>
                  </div>
                </>
              ) : (
                <UserPicker
                  label="For someone else (optional)"
                  id="on-behalf"
                  onPick={(u) => {
                    setOnBehalfOf(u);
                    clearServerError("$on_behalf_of");
                  }}
                />
              )}
              <FieldNote error={fieldErrors.$on_behalf_of} />
            </div>
          )}
        </div>
      </Panel>
    </>
  );

  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
        {attention.length > 0 && <Attention ref={attentionRef} count={attention.length} unplaced={unplaced} />}
        <FormRenderer
          form={form}
          values={values}
          onChange={setValue}
          onBlur={(name) => setTouched((t) => (t.has(name) ? t : new Set(t).add(name)))}
          errors={fieldErrors}
          ctx={{ versionId: form.version_id, projectId: project.id, environment }}
          fieldStates={shownPreview?.fields}
          ref={formRef}
          controlOf={controlOf}
          leadingFields={["$environment"]}
          wizard={{
            check: checkPage,
            checkAll: form.ui_schema.validations?.length ? checkAll : undefined,
            review: requestPanels,
            submit: (
              <SubmitButton
                onClick={submitDraft}
                pending={submit.isPending}
                approval={shownPreview?.approval.required}
              />
            ),
            leadingRows: [
              { label: "Environment", value: environments.find((e) => e.key === environment)?.name ?? environment },
            ],
          }}
          leading={
            <EnvironmentPicker
              environments={environments}
              allowed={project.environments}
              value={environment}
              onChange={(e) => {
                setEnvironment(e);
                setLeaseDays(null);
                clearServerError("$environment");
              }}
              error={fieldErrors.$environment}
            />
          }
        />
        {drawn && (
          <Suspense>
            <RequestTopology graph={drawn} name={item.name} components={shownPreview?.components} />
          </Suspense>
        )}
        {!stepped && requestPanels}
      </div>
      <aside className="flex min-w-0 flex-[1_1_320px] flex-col self-stretch">
        <Summary
          preview={shownPreview}
          previewError={preview.error}
          onRetryPreview={() => void preview.refetch()}
          fetching={preview.isFetching}
          values={withDefaults(values, shownPreview?.fields)}
          pending={submit.isPending}
          formErrors={formErrors}
          onSubmit={stepped ? undefined : submitDraft}
          onSaveDraft={() => saveDraft.mutate()}
          savingDraft={saveDraft.isPending}
          scheduled={later && Boolean(startAt)}
          oneDeployment={Boolean(form.one_deployment)}
          composite={item.type === "composite"}
        />
      </aside>
    </div>
  );
}

export function RequestFormPage({ itemKey, draftId, from }: { itemKey: string; draftId?: string; from?: string }) {
  const { me, projectId, setProjectId } = useCurrentProject();
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
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/catalog">Catalog</Link>
            {item.data && (
              <Link to="/catalog/$itemId" params={{ itemId: item.data.slug }}>
                {item.data.name}
              </Link>
            )}
          </Breadcrumbs>
        }
        title={
          item.data ? (
            <span className="flex items-center gap-3">
              <ItemIcon name={item.data.icon} type={item.data.type} className="size-9" />
              Request {item.data.name}
            </span>
          ) : (
            <Skeleton className="h-5 w-56" />
          )
        }
        description={
          item.data && (
            <>
              {itemTypeLabel[item.data.type]} {item.data.version.number}
              {item.data.source.awx_template_name ? ` · launches ${item.data.source.awx_template_name}` : ""}
              {item.data.typical_duration_seconds
                ? ` · usually done in ${roughDuration(item.data.typical_duration_seconds)}`
                : ""}
            </>
          )
        }
      />
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
          canActForOthers={can(me, "request.on_behalf", projectId)}
        />
      )}
    </Page>
  );
}
