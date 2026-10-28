// Event subscriptions (S10, ADR-0021): webhooks, AWX templates and custom
// workflows that hear about requests, provisioning, day-2 actions,
// deletions and leases; blocking ones may stop a request.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm, q } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, priorityOf, td, th } from "../governance/common";
import { RunDialog } from "./runs";

type S = components["schemas"];
type TargetKind = "webhook" | "awx" | "workflow";

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

function SubscriptionDialog({ sub, onClose }: { sub?: S["Subscription"]; onClose: () => void }) {
  const qc = useQueryClient();
  const topics = useQuery(adm.eventTopics());
  const secrets = useQuery(adm.secrets());
  const templates = useQuery(q.awxTemplates());
  const workflows = useQuery(adm.workflows());
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
  const initialKind: TargetKind = sub?.target.awx ? "awx" : sub?.target.workflow ? "workflow" : "webhook";
  const [kind, setKind] = useState<TargetKind>(initialKind);
  const [url, setUrl] = useState(sub?.target.webhook?.url ?? "");
  const [secret, setSecret] = useState(sub?.target.webhook?.secret ?? "");
  const [template, setTemplate] = useState(sub?.target.awx?.job_template ?? "");
  const [workflow, setWorkflow] = useState(sub?.target.workflow?.key ?? "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const topic = topics.data?.items.find((t) => t.key === f.topic);
  const mayBlock = topic?.may_block ?? false;
  const save = useMutation({
    mutationFn: () => {
      const target: S["SubscriptionTarget"] =
        kind === "webhook"
          ? { webhook: { url, secret } }
          : kind === "awx"
            ? { awx: { job_template: template } }
            : { workflow: { key: workflow } };
      const body: S["SubscriptionWrite"] = {
        name: f.name,
        description: f.description,
        topic: f.topic,
        condition: f.condition,
        blocking: mayBlock && f.blocking,
        on_failure: f.on_failure,
        priority: priorityOf(f.priority),
        timeout_seconds: Number(f.timeout) || 30,
        enabled: f.enabled,
        target,
      };
      return sub
        ? call(api.PUT("/admin/subscriptions/{id}", { params: { path: { id: sub.id } }, body }))
        : call(api.POST("/admin/subscriptions", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
      void qc.invalidateQueries({ queryKey: ["admin", "subscription"] });
      toast.success(`${f.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={sub ? `Change ${sub.name}` : "New subscription"}
        className="w-[min(760px,calc(100vw-32px))]"
      >
        <form
          className="flex max-h-[78vh] flex-col gap-3 overflow-y-auto p-4"
          onSubmit={(e) => {
            e.preventDefault();
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
                options={(topics.data?.items ?? []).map((t) => ({ value: t.key, label: t.key }))}
              />
            </Field>
          </div>
          {topic && <p className="text-xs text-muted">{topic.description}</p>}
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
              className="font-mono text-xs"
              rows={2}
              value={f.condition}
              onChange={(e) => setF({ ...f, condition: e.target.value })}
            />
          </Field>
          {topic && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted">A sample {topic.key} event</summary>
              <pre className="mt-1 max-h-64 overflow-auto rounded bg-sunken p-2 font-mono text-[11px]">
                {JSON.stringify(topic.sample, null, 2)}
              </pre>
            </details>
          )}
          <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
            <legend className="px-1 text-xs font-medium">Target</legend>
            <Field label="Kind of target" htmlFor="sub-kind" error={errors["/target"]}>
              <Select
                id="sub-kind"
                value={kind}
                onChange={(v) => setKind(v as TargetKind)}
                options={[
                  { value: "webhook", label: "Webhook" },
                  { value: "awx", label: "AWX template" },
                  { value: "workflow", label: "Custom workflow" },
                ]}
              />
            </Field>
            {kind === "webhook" && (
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_220px]">
                <Field
                  label="URL"
                  htmlFor="sub-url"
                  error={errors["/target/webhook/url"]}
                  hint="An allowed host (RVAV_WEBHOOK_HOSTS)"
                >
                  <Input
                    id="sub-url"
                    className="font-mono text-xs"
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                  />
                </Field>
                <Field label="Signing secret" htmlFor="sub-secret" error={errors["/target/webhook/secret"]}>
                  <Select
                    id="sub-secret"
                    value={secret}
                    onChange={setSecret}
                    options={(secrets.data?.items ?? []).map((s) => ({ value: s.name, label: s.name }))}
                  />
                </Field>
              </div>
            )}
            {kind === "awx" && (
              <Field
                label="Job template"
                htmlFor="sub-template"
                error={errorAt(errors, "/target/awx")}
                hint="It gets the event as the extra variable rvav_event."
              >
                <Select
                  id="sub-template"
                  value={template}
                  onChange={setTemplate}
                  options={(templates.data?.items ?? [])
                    .filter((t) => !t.workflow)
                    .map((t) => ({ value: t.name, label: t.name }))}
                />
              </Field>
            )}
            {kind === "workflow" && (
              <Field label="Custom workflow" htmlFor="sub-workflow" error={errors["/target/workflow/key"]}>
                <Select
                  id="sub-workflow"
                  value={workflow}
                  onChange={setWorkflow}
                  options={(workflows.data?.items ?? [])
                    .filter((w) => w.usages.includes("subscription_target"))
                    .map((w) => ({ value: w.key ?? "", label: w.title }))}
                />
              </Field>
            )}
          </fieldset>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
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
            <label htmlFor="sub-blocking" className="flex items-center gap-1.5 text-[13px]">
              <Checkbox
                id="sub-blocking"
                disabled={!mayBlock}
                checked={mayBlock && f.blocking}
                onChange={(blocking) => setF({ ...f, blocking })}
              />
              Blocking: the request waits for the answer, which may stop it
              {!mayBlock && <span className="text-xs text-faint">(only on .pre topics)</span>}
            </label>
            {errors["/blocking"] && <p className="text-xs text-bad">{errors["/blocking"]}</p>}
            {mayBlock && f.blocking && (
              <Field label="When it fails or doesn't answer" htmlFor="sub-on-failure" className="max-w-sm">
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
            )}
            <label htmlFor="sub-enabled" className="flex items-center gap-1.5 text-[13px]">
              <Checkbox id="sub-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
              Enabled
            </label>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save subscription
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function SubscriptionsPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = me.global_roles.includes("admin");
  const subs = useQuery(adm.subscriptions());
  const [editing, setEditing] = useState<S["Subscription"] | "new" | null>(null);
  const [tested, setTested] = useState<S["SubscriptionRunDetail"] | null>(null);
  const test = useMutation({
    mutationFn: (id: string) => call(api.POST("/admin/subscriptions/{id}/test", { params: { path: { id } } })),
    onSuccess: (run) => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] });
      setTested(run);
    },
    onError: (e) => failed(e, "The test didn't run"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/subscriptions/{id}", { params: { path: { id } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "subscriptions"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  const list = subs.data?.items ?? [];
  return (
    <Page>
      <PageHeader
        title="Subscriptions"
        meta="Webhooks, AWX templates and custom workflows that hear about requests, provisioning, day-2 actions, deletions and leases."
        actions={
          canEdit && (
            <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
              New subscription
            </Button>
          )
        }
      />
      <Panel title="All subscriptions" flush>
        {subs.isPending && <SkeletonRows rows={3} cols={5} />}
        {subs.isError && <ErrorState error={subs.error} onRetry={() => void subs.refetch()} />}
        {subs.data && list.length === 0 && (
          <EmptyState title="No subscription" body="Nothing hears about rvav's events yet." />
        )}
        {list.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className={`${th} pl-4`}>
                    Subscription
                  </th>
                  <th scope="col" className={th}>
                    Topic
                  </th>
                  <th scope="col" className={th}>
                    Target
                  </th>
                  <th scope="col" className={th}>
                    Last 30 days
                  </th>
                  <th scope="col" className={th}>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((s) => (
                  <tr key={s.id}>
                    <td className={`${td} pl-4`}>
                      <Link
                        to="/admin/subscriptions/$subscriptionId"
                        params={{ subscriptionId: s.id }}
                        className="font-medium hover:text-accent"
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
                      {s.condition && <div className="font-mono text-[11px] text-faint">{s.condition}</div>}
                    </td>
                    <td className={`${td} font-mono text-xs`}>
                      {s.topic}
                      <div className="text-faint">priority {s.priority}</div>
                    </td>
                    <td className={`${td} text-xs`}>{targetText(s.target)}</td>
                    <td className={`${td} text-xs text-muted`}>
                      {s.runs.delivered} delivered, {s.runs.failed} failed, {s.runs.pending} waiting
                    </td>
                    <td className={`${td} text-right whitespace-nowrap`}>
                      {canEdit && (
                        <>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Test ${s.name}`}
                            disabled={test.isPending}
                            onClick={() => test.mutate(s.id)}
                          >
                            Test
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Change ${s.name}`}
                            onClick={() => setEditing(s)}
                          >
                            Change
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Remove ${s.name}`}
                            onClick={() =>
                              window.confirm(`Remove ${s.name} and its run history?`) && remove.mutate(s.id)
                            }
                          >
                            Remove
                          </Button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {editing && <SubscriptionDialog sub={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {tested && <RunDialog runId={tested.id} initial={tested} title="Test delivery" onClose={() => setTested(null)} />}
    </Page>
  );
}
