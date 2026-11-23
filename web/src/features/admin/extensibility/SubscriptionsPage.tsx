// Event subscriptions (S10, ADR-0021): webhooks, AWX templates and custom
// workflows that hear about requests, their approvals, each VM they build or
// delete (ADR-0049), day-2 actions, leases and projects; blocking ones may stop a
// request, or change a VM it hasn't built (ADR-0054).
import type { TFunction } from "i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Webhook } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
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
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm, q } from "@/lib/query";
import { formatList, fullDate, relative } from "@/lib/format";
import { errorAt, failed, fieldErrors, priorityOf, writable } from "../governance/common";
import { RunDialog } from "./runs";

type S = components["schemas"];
type Subscription = S["Subscription"];
type TargetKind = "webhook" | "awx" | "workflow";
type TargetDraft = { kind: TargetKind; url: string; secret: string; template: string; workflow: string };

const tx = i18n.getFixedT(null, "admin-extensibility");

function hostOf(url: string) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function targetText(t: S["SubscriptionTarget"]): string {
  if (t.webhook) return tx("target.webhookTo", { host: hostOf(t.webhook.url) });
  if (t.awx) return tx("target.awx", { name: t.awx.job_template ?? `#${t.awx.template_id ?? ""}` });
  if (t.workflow) return tx("target.workflow", { key: t.workflow.key });
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

/** What a blocking answer may change, and where the answer says it. */
export const changeText = (c: S["SubscriptionChange"]) => tx(`changes.${c}`);

export const runsText = (s: Subscription) =>
  tx("subscriptions.runs", { delivered: s.runs.delivered, failed: s.runs.failed, pending: s.runs.pending });

const invalidate = (qc: ReturnType<typeof useQueryClient>) => {
  void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
  void qc.invalidateQueries({ queryKey: ["admin", "subscription"] });
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
  const { t } = useTranslation("admin-extensibility");
  const secrets = useQuery(adm.secrets());
  const templates = useQuery(q.awxTemplates());
  const workflows = useQuery(adm.workflows());
  const label = (field: "kind" | "url" | "secret" | "template" | "workflow") => t(`target.labels.${of}.${field}`);
  const id = (field: string) => (of === "target" ? `sub-${field}` : `sub-recovery-${field}`);
  const path = `/${of}`;
  const set = (patch: Partial<TargetDraft>) => onChange({ ...value, ...patch });
  return (
    <>
      <Field label={label("kind")} htmlFor={id("kind")} error={errors[path]}>
        <Select
          id={id("kind")}
          value={value.kind}
          onChange={(v) => set({ kind: v as TargetKind })}
          options={[
            { value: "webhook", label: t("target.kinds.webhook") },
            { value: "awx", label: t("target.kinds.awx") },
            { value: "workflow", label: t("target.kinds.workflow") },
          ]}
        />
      </Field>
      {value.kind === "webhook" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_220px]">
          <Field
            label={label("url")}
            htmlFor={id("url")}
            error={errors[`${path}/webhook/url`]}
            hint={t("target.urlHint")}
          >
            <Input
              id={id("url")}
              className="font-mono text-secondary"
              value={value.url}
              onChange={(e) => set({ url: e.target.value })}
            />
          </Field>
          <Field label={label("secret")} htmlFor={id("secret")} error={errors[`${path}/webhook/secret`]}>
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
          label={label("template")}
          htmlFor={id("template")}
          error={errorAt(errors, `${path}/awx`)}
          hint={t("target.awxHint")}
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
        <Field label={label("workflow")} htmlFor={id("workflow")} error={errors[`${path}/workflow/key`]}>
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
  const { t } = useTranslation("admin-extensibility");
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
  const changes = (topic?.may_change ?? []).map(changeText);
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
      toast.success(t(sub ? "shared.saved" : "shared.created", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("shared.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={sub ? t("shared.changeName", { name: sub.name }) : t("subscriptions.new")}
      description={t("subscriptions.sheet.lead")}
      className="w-[min(720px,100vw)]"
      submitLabel={
        save.isPending ? t("shared.saving") : t(sub ? "subscriptions.sheet.save" : "subscriptions.sheet.create")
      }
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_220px]">
        <Field label={t("shared.name")} htmlFor="sub-name" error={errors["/name"]}>
          <Input id="sub-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label={t("shared.topic")} htmlFor="sub-topic" error={errors["/topic"]}>
          <Select
            id="sub-topic"
            value={f.topic}
            onChange={(v) => setF({ ...f, topic: v as S["EventTopicKey"] })}
            options={(topics.data?.items ?? []).map((ev) => ({
              value: ev.key,
              label: ev.key,
              group: t(`topicGroups.${ev.group}`),
            }))}
          />
        </Field>
      </div>
      {topic && <p className="-mt-2 text-secondary text-muted">{topic.description}</p>}
      <Field label={t("shared.description")} htmlFor="sub-description">
        <Input
          id="sub-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Field
        label={t("subscriptions.sheet.condition")}
        htmlFor="sub-condition"
        error={errors["/condition"]}
        hint={t("subscriptions.sheet.conditionHint")}
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
          <summary className="cursor-pointer text-muted">
            {t("subscriptions.sheet.sample", { topic: topic.key })}
          </summary>
          <pre className="mt-1 max-h-64 overflow-auto rounded bg-sunken p-2 font-mono text-caption">
            {JSON.stringify(topic.sample, null, 2)}
          </pre>
        </details>
      )}
      <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">{t("shared.target")}</legend>
        <TargetFields of="target" value={target} onChange={setTarget} errors={errors} />
      </fieldset>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("shared.priority")} htmlFor="sub-priority" hint={t("subscriptions.sheet.priorityHint")}>
          <Input
            id="sub-priority"
            type="number"
            value={f.priority}
            onChange={(e) => setF({ ...f, priority: e.target.value })}
          />
        </Field>
        <Field label={t("subscriptions.sheet.timeout")} htmlFor="sub-timeout" error={errors["/timeout_seconds"]}>
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
          {t("subscriptions.sheet.blocking")}
          {!mayBlock && blockable.length > 0 && (
            <span className="text-secondary text-faint">
              {t("subscriptions.sheet.onlyOn", { topics: formatList(blockable) })}
            </span>
          )}
        </label>
        {errors["/blocking"] && <p className="text-secondary text-bad">{errors["/blocking"]}</p>}
        {blocks && (
          <>
            <p className="text-secondary text-muted">
              {changes.length > 0 ? `${t("subscriptions.sheet.mayChange", { changes: formatList(changes) })} ` : ""}
              {t("subscriptions.sheet.answers")}
            </p>
            <Field label={t("subscriptions.sheet.onFailure")} htmlFor="sub-on-failure" className="max-w-sm">
              <Select
                id="sub-on-failure"
                value={f.on_failure}
                onChange={(v) => setF({ ...f, on_failure: v as S["SubscriptionOnFailure"] })}
                options={[
                  { value: "stop_request", label: t("subscriptions.sheet.stop") },
                  { value: "continue_request", label: t("subscriptions.sheet.goOn") },
                ]}
              />
            </Field>
            <label htmlFor="sub-recovers" className="flex items-center gap-1.5 text-body">
              <Checkbox id="sub-recovers" checked={recovers} onChange={setRecovers} />
              {t("subscriptions.sheet.recovers")}
            </label>
            {errors["/recovery"] && !recovers && <p className="text-secondary text-bad">{errors["/recovery"]}</p>}
            {recovers && (
              <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
                <legend className="px-1 text-secondary font-medium">{t("subscriptions.sheet.recovery")}</legend>
                <p className="text-secondary text-muted">{t("subscriptions.sheet.recoveryLead")}</p>
                <TargetFields of="recovery" value={recovery} onChange={setRecovery} errors={errors} />
              </fieldset>
            )}
          </>
        )}
        <label htmlFor="sub-enabled" className="flex items-center gap-1.5 text-body">
          <Checkbox id="sub-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
          {t("shared.enabled")}
        </label>
      </div>
    </Sheet>
  );
}

/** Sends a subscription its topic's sample event, then shows how the delivery went. */
export function useSubscriptionTest() {
  const { t } = useTranslation("admin-extensibility");
  const qc = useQueryClient();
  const [tested, setTested] = useState<S["SubscriptionRunDetail"] | null>(null);
  const test = useMutation({
    mutationFn: (id: string) => call(api.POST("/admin/subscriptions/{id}/test", { params: { path: { id } } })),
    onSuccess: (run) => {
      invalidate(qc);
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-runs"] });
      setTested(run);
    },
    onError: (e) => failed(e, t("subscriptions.testFailed")),
  });
  const dialog = tested && (
    <RunDialog runId={tested.id} initial={tested} title={t("run.testTitle")} onClose={() => setTested(null)} />
  );
  return { test, dialog };
}

/** Turns a subscription on or off, keeping the rest of it. */
export function useSubscriptionSwitch() {
  const { t } = useTranslation("admin-extensibility");
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
      toast.success(s.enabled ? t("shared.isOn", { name: s.name }) : t("shared.isOff", { name: s.name }));
    },
    onError: (e) => failed(e, t("shared.notSaved")),
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
  const { t } = useTranslation("admin-extensibility");
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/subscriptions/{id}", { params: { path: { id: sub.id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
      toast.success(t("shared.removed", { name: sub.name }));
      onClose();
      onRemoved?.();
    },
    onError: (e) => failed(e, t("shared.notRemoved")),
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("shared.removeName", { name: sub.name })}
      description={t("subscriptions.remove.body")}
      consequences={
        sub.blocking
          ? t("subscriptions.remove.blocking", { topic: sub.topic })
          : t("subscriptions.remove.stops", { target: targetText(sub.target), topic: sub.topic })
      }
      confirmLabel={t("subscriptions.remove.confirm")}
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
    />
  );
}

const columnsFor = (t: TFunction<"admin-extensibility">): Column<Subscription>[] => [
  {
    id: "name",
    header: t("subscriptions.columns.subscription"),
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
            {t("shared.blocking")}
          </Badge>
        )}
        {!s.enabled && (
          <Badge tone="neutral" className="ml-2">
            {t("shared.off")}
          </Badge>
        )}
        {s.condition && <div className="font-mono text-caption text-faint">{s.condition}</div>}
      </>
    ),
  },
  {
    id: "topic",
    header: t("shared.topic"),
    sortValue: (s) => s.topic,
    cell: (s) => (
      <div className="font-mono text-secondary">
        {s.topic}
        <div className="text-faint">{t("subscriptions.columns.priority", { priority: s.priority })}</div>
      </div>
    ),
  },
  {
    id: "target",
    header: t("shared.target"),
    cell: (s) => <span className="text-secondary">{targetText(s.target)}</span>,
  },
  {
    id: "runs",
    header: t("shared.last30"),
    sortValue: (s) => s.last_run_at,
    cell: (s) => (
      <div className="text-secondary text-muted">
        {runsText(s)}
        {s.last_run_at && (
          <div className="text-faint" title={fullDate(s.last_run_at)}>
            {t("subscriptions.columns.lastRun", { when: relative(s.last_run_at) })}
          </div>
        )}
      </div>
    ),
  },
];

export function SubscriptionsPage() {
  const { t } = useTranslation(["admin-extensibility", "shell"]);
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const subs = useQuery(adm.subscriptions());
  const [editing, setEditing] = useState<Subscription | "new" | null>(null);
  const [removing, setRemoving] = useState<Subscription | null>(null);
  const { test, dialog } = useSubscriptionTest();
  const toggle = useSubscriptionSwitch();
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      {t("subscriptions.new")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.subscriptions")}
        description={t("subscriptions.lead")}
        actions={subs.data?.items.length ? create : undefined}
      />
      <Panel title={t("subscriptions.all")} flush>
        <DataTable
          label={t("subscriptions.label")}
          columns={columnsFor(t)}
          rows={subs.data?.items}
          rowKey={(s) => s.id}
          rowLinks
          minWidth={820}
          loading={subs.isPending}
          error={subs.isError && <ErrorState error={subs.error} onRetry={() => void subs.refetch()} />}
          empty={
            <EmptyState
              icon={Webhook}
              title={t("subscriptions.none")}
              body={t("subscriptions.noneBody")}
              action={create || undefined}
            />
          }
          rowMenuLabel={(s) => t("shared.actionsFor", { name: s.name })}
          rowMenu={
            canEdit
              ? (s) => (
                  <>
                    <MenuItem disabled={test.isPending} onSelect={() => test.mutate(s.id)}>
                      {t("subscriptions.sendTest")}
                    </MenuItem>
                    <MenuItem onSelect={() => setEditing(s)}>{t("shared.change")}</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(s)}>
                      {s.enabled ? t("shared.turnOff") : t("shared.turnOn")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(s)}>
                      {t("shared.remove")}
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
