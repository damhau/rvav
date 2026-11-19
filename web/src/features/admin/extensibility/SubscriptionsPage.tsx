// Event subscriptions (S10, ADR-0021): webhooks, AWX templates and custom
// workflows that hear about requests, their approvals, each VM they build or
// delete (ADR-0049), day-2 actions, leases and projects; blocking ones may stop a
// request, or change a VM it hasn't built (ADR-0054).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Webhook } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm, q } from "@/lib/query";
import { fullDate, relative } from "@/lib/utils";
import { errorAt, failed, fieldErrors, priorityOf, writable } from "../governance/common";
import { RunDialog } from "./runs";

type S = components["schemas"];
type Subscription = S["Subscription"];
type TargetKind = "webhook" | "awx" | "workflow";
type TargetDraft = { kind: TargetKind; url: string; secret: string; template: string; workflow: string };

export function targetText(t: S["SubscriptionTarget"]): string {
  if (t.webhook) {
    try {
      return "Webhook to " + new URL(t.webhook.url).host;
    } catch {
      return "Webhook to " + t.webhook.url;
    }
  }
  if (t.awx) return "AWX template " + (t.awx.job_template ?? `#${t.awx.template_id ?? ""}`);
  if (t.workflow) return "Custom workflow " + t.workflow.key;
  return "—";
}

const draftOf = (t?: S["SubscriptionTarget"]): TargetDraft => ({
  kind: t?.awx ? "awx" : t?.workflow ? "workflow" : "webhook",
  url: t?.webhook?.url ?? "",
  secret: t?.webhook?.secret ?? "",
  template: t?.awx?.job_template ?? "",
  workflow: t?.workflow?.key ?? "",
});

const targetOf = (d: TargetDraft): S["SubscriptionTarget"] =>
  d.kind === "webhook"
    ? { webhook: { url: d.url, secret: d.secret } }
    : d.kind === "awx"
      ? { awx: { job_template: d.template } }
      : { workflow: { key: d.workflow } };

/** "a, b and c". */
export const listText = (items: string[]) =>
  items.length < 2 ? items.join("") : `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`;

/** What a blocking answer may change, and where the answer says it. */
export const changeText: Record<S["SubscriptionChange"], string> = {
  properties: "the deployment's custom properties (properties)",
  name: "the VM's name (machine.name)",
  placement: "its cluster, resource pool, datastore and folder (machine.placement)",
  networks: "its adapters' networks and addresses (machine.networks)",
};

export const runsText = (s: Subscription) =>
  `${s.runs.delivered} delivered, ${s.runs.failed} failed, ${s.runs.pending} waiting`;

const topicGroups: Record<S["EventTopicGroup"], string> = {
  request: "Requests",
  approval: "A request's approval levels",
  machine: "A request's VMs, one event each",
  custom: "A request's custom resources, one event each",
  lease: "Leases",
  project: "Projects",
};

const invalidate = (qc: ReturnType<typeof useQueryClient>) => {
  void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
  void qc.invalidateQueries({ queryKey: ["admin", "subscription"] });
};

const targetLabels = {
  target: {
    kind: "Kind of target",
    url: "URL",
    secret: "Signing secret",
    template: "Job template",
    workflow: "Custom workflow",
  },
  recovery: {
    kind: "Kind of recovery target",
    url: "Recovery URL",
    secret: "Recovery signing secret",
    template: "Recovery job template",
    workflow: "Recovery workflow",
  },
};

/** A subscription's target, or its recovery target: a webhook, an AWX template or a custom workflow. */
function TargetFields({
  of,
  value,
  onChange,
  errors,
}: {
  of: "target" | "recovery";
  value: TargetDraft;
  onChange: (d: TargetDraft) => void;
  errors: Record<string, string>;
}) {
  const secrets = useQuery(adm.secrets());
  const templates = useQuery(q.awxTemplates());
  const workflows = useQuery(adm.workflows());
  const label = targetLabels[of];
  const id = (field: string) => (of === "target" ? `sub-${field}` : `sub-recovery-${field}`);
  const path = `/${of}`;
  const set = (patch: Partial<TargetDraft>) => onChange({ ...value, ...patch });
  return (
    <>
      <Field label={label.kind} htmlFor={id("kind")} error={errors[path]}>
        <Select
          id={id("kind")}
          value={value.kind}
          onChange={(v) => set({ kind: v as TargetKind })}
          options={[
            { value: "webhook", label: "Webhook" },
            { value: "awx", label: "AWX template" },
            { value: "workflow", label: "Custom workflow" },
          ]}
        />
      </Field>
      {value.kind === "webhook" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_220px]">
          <Field
            label={label.url}
            htmlFor={id("url")}
            error={errors[`${path}/webhook/url`]}
            hint="On a host rvav may call, as Integrations sets"
          >
            <Input
              id={id("url")}
              className="font-mono text-secondary"
              value={value.url}
              onChange={(e) => set({ url: e.target.value })}
            />
          </Field>
          <Field label={label.secret} htmlFor={id("secret")} error={errors[`${path}/webhook/secret`]}>
            <Select
              id={id("secret")}
              value={value.secret}
              onChange={(secret) => set({ secret })}
              options={(secrets.data?.items ?? [])
                .filter((s) => !s.project_id)
                .map((s) => ({ value: s.name, label: s.name }))}
            />
          </Field>
        </div>
      )}
      {value.kind === "awx" && (
        <Field
          label={label.template}
          htmlFor={id("template")}
          error={errorAt(errors, `${path}/awx`)}
          hint="It gets the event as the extra variable rvav_event."
        >
          <Select
            id={id("template")}
            value={value.template}
            onChange={(template) => set({ template })}
            options={(templates.data?.items ?? [])
              .filter((t) => !t.workflow)
              .map((t) => ({ value: t.name, label: t.name }))}
          />
        </Field>
      )}
      {value.kind === "workflow" && (
        <Field label={label.workflow} htmlFor={id("workflow")} error={errors[`${path}/workflow/key`]}>
          <Select
            id={id("workflow")}
            value={value.workflow}
            onChange={(workflow) => set({ workflow })}
            options={(workflows.data?.items ?? [])
              .filter((w) => w.usages.includes("subscription_target"))
              .map((w) => ({ value: w.key ?? "", label: w.title }))}
          />
        </Field>
      )}
    </>
  );
}

export function SubscriptionSheet({ sub, onClose }: { sub?: Subscription; onClose: () => void }) {
  const qc = useQueryClient();
  const topics = useQuery(adm.eventTopics());
  const [f, setF] = useState({
    name: sub?.name ?? "",
    description: sub?.description ?? "",
    topic: sub?.topic ?? "provisioning.post",
    condition: sub?.condition ?? "",
    blocking: sub?.blocking ?? false,
    on_failure: sub?.on_failure ?? "stop_request",
    priority: String(sub?.priority ?? 100),
    timeout: String(sub?.timeout_seconds ?? 30),
    enabled: sub?.enabled ?? true,
  });
  const [target, setTarget] = useState(() => draftOf(sub?.target));
  const [recovers, setRecovers] = useState(Boolean(sub?.recovery));
  const [recovery, setRecovery] = useState(() => draftOf(sub?.recovery));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const topic = topics.data?.items.find((t) => t.key === f.topic);
  const mayBlock = topic?.may_block ?? false;
  const blocks = mayBlock && f.blocking;
  const blockable = (topics.data?.items ?? []).filter((t) => t.may_block).map((t) => t.key);
  const changes = (topic?.may_change ?? []).map((c) => changeText[c]);
  const save = useMutation({
    mutationFn: () => {
      const body: S["SubscriptionWrite"] = {
        name: f.name,
        description: f.description,
        topic: f.topic,
        condition: f.condition,
        blocking: blocks,
        on_failure: f.on_failure,
        priority: priorityOf(f.priority),
        timeout_seconds: Number(f.timeout) || 30,
        enabled: f.enabled,
        target: targetOf(target),
        ...(blocks && recovers ? { recovery: targetOf(recovery) } : {}),
      };
      return sub
        ? call(api.PUT("/admin/subscriptions/{id}", { params: { path: { id: sub.id } }, body }))
        : call(api.POST("/admin/subscriptions", { body }));
    },
    onSuccess: () => {
      invalidate(qc);
      toast.success(`${f.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={sub ? `Change ${sub.name}` : "New subscription"}
      description="What it hears about, and what is told."
      className="w-[min(720px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save subscription"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_220px]">
        <Field label="Name" htmlFor="sub-name" error={errors["/name"]}>
          <Input id="sub-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label="Topic" htmlFor="sub-topic" error={errors["/topic"]}>
          <Select
            id="sub-topic"
            value={f.topic}
            onChange={(v) => setF({ ...f, topic: v as S["EventTopicKey"] })}
            options={(topics.data?.items ?? []).map((t) => ({
              value: t.key,
              label: t.key,
              group: topicGroups[t.group],
            }))}
          />
        </Field>
      </div>
      {topic && <p className="-mt-2 text-secondary text-muted">{topic.description}</p>}
      <Field label="Description" htmlFor="sub-description">
        <Input
          id="sub-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Field
        label="Only when (CEL)"
        htmlFor="sub-condition"
        error={errors["/condition"]}
        hint={
          'Over the event\'s data; empty for every event. For example environment == "prd" && item.slug == "rhel9-server"'
        }
      >
        <Textarea
          id="sub-condition"
          className="font-mono text-secondary"
          rows={2}
          value={f.condition}
          onChange={(e) => setF({ ...f, condition: e.target.value })}
        />
      </Field>
      {topic && (
        <details className="text-secondary">
          <summary className="cursor-pointer text-muted">A sample {topic.key} event</summary>
          <pre className="mt-1 max-h-64 overflow-auto rounded bg-sunken p-2 font-mono text-caption">
            {JSON.stringify(topic.sample, null, 2)}
          </pre>
        </details>
      )}
      <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">Target</legend>
        <TargetFields of="target" value={target} onChange={setTarget} errors={errors} />
      </fieldset>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Priority" htmlFor="sub-priority" hint="Lower first">
          <Input
            id="sub-priority"
            type="number"
            value={f.priority}
            onChange={(e) => setF({ ...f, priority: e.target.value })}
          />
        </Field>
        <Field label="Timeout (s)" htmlFor="sub-timeout" error={errors["/timeout_seconds"]}>
          <Input
            id="sub-timeout"
            type="number"
            min={1}
            max={3600}
            value={f.timeout}
            onChange={(e) => setF({ ...f, timeout: e.target.value })}
          />
        </Field>
      </div>
      <div className="flex flex-col gap-2">
        <label htmlFor="sub-blocking" className="flex items-center gap-1.5 text-body">
          <Checkbox
            id="sub-blocking"
            disabled={!mayBlock}
            checked={blocks}
            onChange={(blocking) => setF({ ...f, blocking })}
          />
          Blocking: the request waits for the answer, which may stop it
          {!mayBlock && blockable.length > 0 && (
            <span className="text-secondary text-faint">(only on {listText(blockable)})</span>
          )}
        </label>
        {errors["/blocking"] && <p className="text-secondary text-bad">{errors["/blocking"]}</p>}
        {blocks && (
          <>
            <p className="text-secondary text-muted">
              {changes.length > 0
                ? `Its answer may also change ${listText(changes)}; rvav checks the change again before building, as it checked the request. `
                : ""}
              A webhook answers with JSON, an AWX job with the rvav_answer artifact (set_stats), a custom workflow with
              its result.
            </p>
            <Field
              label="When it fails, doesn't answer or asks for a change rvav refuses"
              htmlFor="sub-on-failure"
              className="max-w-sm"
            >
              <Select
                id="sub-on-failure"
                value={f.on_failure}
                onChange={(v) => setF({ ...f, on_failure: v as S["SubscriptionOnFailure"] })}
                options={[
                  { value: "stop_request", label: "Stop the request" },
                  { value: "continue_request", label: "Let the request go on" },
                ]}
              />
            </Field>
            <label htmlFor="sub-recovers" className="flex items-center gap-1.5 text-body">
              <Checkbox id="sub-recovers" checked={recovers} onChange={setRecovers} />
              Tell a recovery target when it fails
            </label>
            {errors["/recovery"] && !recovers && <p className="text-secondary text-bad">{errors["/recovery"]}</p>}
            {recovers && (
              <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
                <legend className="px-1 text-secondary font-medium">Recovery target</legend>
                <p className="text-secondary text-muted">
                  Told once, with the event and why it failed (recovery.reason). If it answers stop or continue, that
                  decides; otherwise the setting above does.
                </p>
                <TargetFields of="recovery" value={recovery} onChange={setRecovery} errors={errors} />
              </fieldset>
            )}
          </>
        )}
        <label htmlFor="sub-enabled" className="flex items-center gap-1.5 text-body">
          <Checkbox id="sub-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
          Enabled
        </label>
      </div>
    </Sheet>
  );
}

/** Sends a subscription its topic's sample event, then shows how the delivery went. */
export function useSubscriptionTest() {
  const qc = useQueryClient();
  const [tested, setTested] = useState<S["SubscriptionRunDetail"] | null>(null);
  const test = useMutation({
    mutationFn: (id: string) => call(api.POST("/admin/subscriptions/{id}/test", { params: { path: { id } } })),
    onSuccess: (run) => {
      invalidate(qc);
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-runs"] });
      setTested(run);
    },
    onError: (e) => failed(e, "The test didn't run"),
  });
  const dialog = tested && (
    <RunDialog runId={tested.id} initial={tested} title="Test delivery" onClose={() => setTested(null)} />
  );
  return { test, dialog };
}

/** Turns a subscription on or off, keeping the rest of it. */
export function useSubscriptionSwitch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (s: Subscription) =>
      call(
        api.PUT("/admin/subscriptions/{id}", {
          params: { path: { id: s.id } },
          body: { ...writable(s, "id", "created_at", "updated_at", "runs", "last_run_at"), enabled: !s.enabled },
        }),
      ),
    onSuccess: (s) => {
      invalidate(qc);
      toast.success(s.enabled ? `${s.name} is on` : `${s.name} is off`);
    },
    onError: (e) => failed(e, "Not saved"),
  });
}

export function RemoveSubscription({
  sub,
  onClose,
  onRemoved,
}: {
  sub: Subscription;
  onClose: () => void;
  onRemoved?: () => void;
}) {
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/subscriptions/{id}", { params: { path: { id: sub.id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
      toast.success(`${sub.name} removed`);
      onClose();
      onRemoved?.();
    },
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Remove ${sub.name}?`}
      description="Its runs, with what each sent and got back, go with it. To keep them, turn it off instead."
      consequences={
        sub.blocking
          ? `Requests stop waiting for its answer on ${sub.topic}.`
          : `${targetText(sub.target)} stops hearing about ${sub.topic}.`
      }
      confirmLabel="Remove subscription"
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
    />
  );
}

const columns: Column<Subscription>[] = [
  {
    id: "name",
    header: "Subscription",
    sortValue: (s) => s.name,
    cell: (s) => (
      <>
        <Link
          to="/admin/subscriptions/$subscriptionId"
          params={{ subscriptionId: s.id }}
          className="font-medium text-fg hover:text-accent"
        >
          {s.name}
        </Link>
        {s.blocking && (
          <Badge tone="warn" className="ml-2">
            Blocking
          </Badge>
        )}
        {!s.enabled && (
          <Badge tone="neutral" className="ml-2">
            Off
          </Badge>
        )}
        {s.condition && <div className="font-mono text-caption text-faint">{s.condition}</div>}
      </>
    ),
  },
  {
    id: "topic",
    header: "Topic",
    sortValue: (s) => s.topic,
    cell: (s) => (
      <div className="font-mono text-secondary">
        {s.topic}
        <div className="text-faint">priority {s.priority}</div>
      </div>
    ),
  },
  { id: "target", header: "Target", cell: (s) => <span className="text-secondary">{targetText(s.target)}</span> },
  {
    id: "runs",
    header: "Last 30 days",
    sortValue: (s) => s.last_run_at,
    cell: (s) => (
      <div className="text-secondary text-muted">
        {runsText(s)}
        {s.last_run_at && (
          <div className="text-faint" title={fullDate(s.last_run_at)}>
            Last run {relative(s.last_run_at)}
          </div>
        )}
      </div>
    ),
  },
];

export function SubscriptionsPage() {
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const subs = useQuery(adm.subscriptions());
  const [editing, setEditing] = useState<Subscription | "new" | null>(null);
  const [removing, setRemoving] = useState<Subscription | null>(null);
  const { test, dialog } = useSubscriptionTest();
  const toggle = useSubscriptionSwitch();
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      New subscription
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Subscriptions"
        description="Webhooks, AWX templates and custom workflows that hear about requests, their approvals, each VM a request builds or deletes, day-2 actions, leases and projects. A blocking one may stop a request."
        actions={subs.data?.items.length ? create : undefined}
      />
      <Panel title="All subscriptions" flush>
        <DataTable
          label="Subscriptions"
          columns={columns}
          rows={subs.data?.items}
          rowKey={(s) => s.id}
          rowLinks
          minWidth={820}
          loading={subs.isPending}
          error={subs.isError && <ErrorState error={subs.error} onRetry={() => void subs.refetch()} />}
          empty={
            <EmptyState
              icon={Webhook}
              title="No subscription yet"
              body="Subscribe a webhook, an AWX template or a custom workflow to a topic, such as a VM built, to tell another system."
              action={create || undefined}
            />
          }
          rowMenuLabel={(s) => `Actions for ${s.name}`}
          rowMenu={
            canEdit
              ? (s) => (
                  <>
                    <MenuItem disabled={test.isPending} onSelect={() => test.mutate(s.id)}>
                      Send a test event
                    </MenuItem>
                    <MenuItem onSelect={() => setEditing(s)}>Change</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(s)}>
                      {s.enabled ? "Turn off" : "Turn on"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(s)}>
                      Remove
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      {editing && <SubscriptionSheet sub={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && <RemoveSubscription sub={removing} onClose={() => setRemoving(null)} />}
      {dialog}
    </Page>
  );
}
