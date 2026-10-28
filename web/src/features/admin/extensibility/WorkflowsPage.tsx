// Custom Temporal workflows (S10, ADR-0022): registered here, they run as
// catalog items, day-2 actions, subscription targets or composite steps.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, td, th } from "../governance/common";

type S = components["schemas"];
type Usage = S["WorkflowUsage"];
type Day2Type = S["WorkflowDay2"]["types"][number];

export const usageLabel: Record<Usage, string> = {
  catalog_item: "Catalog item",
  day2_action: "Day-2 action",
  subscription_target: "Subscription target",
  composite_step: "Composite step",
};

const days = (seconds: number) => {
  const d = seconds / 86400;
  return d >= 1 ? `${Math.round(d * 10) / 10} days` : `${Math.round(seconds / 3600)} h`;
};

const sampleSchema = `{
  "type": "object",
  "required": ["application"],
  "properties": {
    "application": { "type": "string", "title": "Application" }
  }
}`;

function WorkflowDialog({ wf, onClose }: { wf?: S["WorkflowRegistration"]; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useQuery(adm.projects("", false));
  const [f, setF] = useState({
    key: wf?.key ?? "",
    title: wf?.title ?? "",
    description: wf?.description ?? "",
    workflow_type: wf?.workflow_type ?? "",
    task_queue: wf?.task_queue ?? "",
    timeout: String((wf?.timeout_seconds ?? 2592000) / 86400),
    enabled: wf?.enabled ?? true,
  });
  const [schema, setSchema] = useState(wf?.input_schema ? JSON.stringify(wf.input_schema, null, 2) : sampleSchema);
  const [usages, setUsages] = useState<Usage[]>(wf?.usages ?? ["catalog_item"]);
  const [projectIds, setProjectIds] = useState<string[]>(wf?.project_ids ?? []);
  const [day2, setDay2] = useState<S["WorkflowDay2"]>(
    wf?.day2 ?? { name: "", types: ["deployment"], condition: "", offered: false },
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      let input_schema: Record<string, unknown>;
      try {
        input_schema = JSON.parse(schema) as Record<string, unknown>;
      } catch {
        return Promise.reject(new SyntaxError("input_schema"));
      }
      const body: S["WorkflowRegistrationWrite"] = {
        key: f.key,
        title: f.title,
        description: f.description,
        workflow_type: f.workflow_type,
        task_queue: f.task_queue,
        input_schema,
        timeout_seconds: Math.round(Number(f.timeout) * 86400) || 2592000,
        project_ids: projectIds,
        usages,
        day2: usages.includes("day2_action") ? day2 : undefined,
        enabled: f.enabled,
      };
      return wf
        ? call(api.PUT("/admin/workflows/{key}", { params: { path: { key: wf.key ?? "" } }, body }))
        : call(api.POST("/admin/workflows", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
      toast.success(`${f.title} saved`);
      onClose();
    },
    onError: (e) => {
      if (e instanceof SyntaxError) {
        setErrors({ "/input_schema": "This is not valid JSON." });
        return;
      }
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const toggle = <T,>(list: T[], v: T, on: boolean) => (on ? [...list, v] : list.filter((x) => x !== v));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={wf ? `Change ${wf.title}` : "Register a workflow"}
        className="w-[min(820px,calc(100vw-32px))]"
      >
        <form
          className="flex max-h-[78vh] flex-col gap-3 overflow-y-auto p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[200px_1fr]">
            <Field
              label="Key"
              htmlFor="wf-key"
              error={errors["/key"]}
              hint={wf ? "Set when registered" : "Its name in rvav"}
            >
              <Input
                id="wf-key"
                className="font-mono"
                disabled={!!wf}
                value={f.key}
                onChange={(e) => setF({ ...f, key: e.target.value })}
              />
            </Field>
            <Field label="Title" htmlFor="wf-title" error={errors["/title"]}>
              <Input id="wf-title" value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} />
            </Field>
          </div>
          <Field label="Description" htmlFor="wf-description">
            <Input
              id="wf-description"
              value={f.description}
              onChange={(e) => setF({ ...f, description: e.target.value })}
            />
          </Field>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr_140px]">
            <Field
              label="Workflow type"
              htmlFor="wf-type"
              error={errors["/workflow_type"]}
              hint="Ends with its major version: decommission.v1"
            >
              <Input
                id="wf-type"
                className="font-mono"
                value={f.workflow_type}
                onChange={(e) => setF({ ...f, workflow_type: e.target.value })}
              />
            </Field>
            <Field label="Task queue" htmlFor="wf-queue" error={errors["/task_queue"]}>
              <Input
                id="wf-queue"
                className="font-mono"
                value={f.task_queue}
                onChange={(e) => setF({ ...f, task_queue: e.target.value })}
              />
            </Field>
            <Field label="Timeout (days)" htmlFor="wf-timeout" error={errors["/timeout_seconds"]}>
              <Input
                id="wf-timeout"
                type="number"
                min={0}
                step="any"
                value={f.timeout}
                onChange={(e) => setF({ ...f, timeout: e.target.value })}
              />
            </Field>
          </div>
          <Field
            label="Input schema (JSON Schema)"
            htmlFor="wf-schema"
            error={errorAt(errors, "/input_schema")}
            hint="The form people fill when they start it."
          >
            <Textarea
              id="wf-schema"
              className="font-mono text-xs"
              rows={8}
              value={schema}
              onChange={(e) => setSchema(e.target.value)}
            />
          </Field>
          <fieldset className="flex flex-col gap-1.5">
            <legend className="text-xs font-medium">Used as</legend>
            <div className="flex flex-wrap gap-4">
              {(Object.keys(usageLabel) as Usage[]).map((u) => (
                <label key={u} htmlFor={`wf-usage-${u}`} className="flex items-center gap-1.5 text-[13px]">
                  <Checkbox
                    id={`wf-usage-${u}`}
                    checked={usages.includes(u)}
                    onChange={(on) => setUsages(toggle(usages, u, on))}
                  />
                  {usageLabel[u]}
                </label>
              ))}
            </div>
            {errors["/usages"] && <p className="text-xs text-bad">{errors["/usages"]}</p>}
          </fieldset>
          {usages.includes("day2_action") && (
            <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
              <legend className="px-1 text-xs font-medium">As the day-2 action wf.{f.key || "<key>"}</legend>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr]">
                <Field label="Action name" htmlFor="wf-day2-name" error={errors["/day2/name"]}>
                  <Input
                    id="wf-day2-name"
                    value={day2.name}
                    onChange={(e) => setDay2({ ...day2, name: e.target.value })}
                  />
                </Field>
                <fieldset className="flex flex-col gap-1.5">
                  <legend className="text-xs font-medium">Runs on</legend>
                  <div className="flex gap-4">
                    {(["vsphere.vm", "deployment"] as Day2Type[]).map((t) => (
                      <label key={t} htmlFor={`wf-day2-${t}`} className="flex items-center gap-1.5 text-[13px]">
                        <Checkbox
                          id={`wf-day2-${t}`}
                          checked={day2.types.includes(t)}
                          onChange={(on) => setDay2({ ...day2, types: toggle(day2.types, t, on) })}
                        />
                        {t === "vsphere.vm" ? "VMs" : "Deployments"}
                      </label>
                    ))}
                  </div>
                  {errors["/day2/types"] && <p className="text-xs text-bad">{errors["/day2/types"]}</p>}
                </fieldset>
              </div>
              <Field
                label="Offered when (CEL)"
                htmlFor="wf-day2-condition"
                error={errors["/day2/condition"]}
                hint="Empty: everywhere"
              >
                <Input
                  id="wf-day2-condition"
                  className="font-mono text-xs"
                  value={day2.condition ?? ""}
                  onChange={(e) => setDay2({ ...day2, condition: e.target.value })}
                />
              </Field>
              <label htmlFor="wf-day2-offered" className="flex items-center gap-1.5 text-[13px]">
                <Checkbox
                  id="wf-day2-offered"
                  checked={day2.offered}
                  onChange={(offered) => setDay2({ ...day2, offered })}
                />
                Offer it on deployments
              </label>
            </fieldset>
          )}
          <fieldset className="flex flex-col gap-1.5">
            <legend className="text-xs font-medium">Projects</legend>
            <p className="text-xs text-muted">None ticked: every project may use it.</p>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
              {(projects.data?.items ?? []).map((p) => (
                <label key={p.id} htmlFor={`wf-project-${p.id}`} className="flex items-center gap-1.5 text-[13px]">
                  <Checkbox
                    id={`wf-project-${p.id}`}
                    checked={projectIds.includes(p.id)}
                    onChange={(on) => setProjectIds(toggle(projectIds, p.id, on))}
                  />
                  {p.name}
                </label>
              ))}
            </div>
            {errorAt(errors, "/project_ids") && <p className="text-xs text-bad">{errorAt(errors, "/project_ids")}</p>}
          </fieldset>
          <label htmlFor="wf-enabled" className="flex items-center gap-1.5 text-[13px]">
            <Checkbox id="wf-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
            Enabled
          </label>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save workflow
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function WorkflowsPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = me.global_roles.includes("admin");
  const workflows = useQuery(adm.workflows());
  const [editing, setEditing] = useState<S["WorkflowRegistration"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/workflows/{key}", { params: { path: { key } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "workflows"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  const list = workflows.data?.items ?? [];
  return (
    <Page>
      <PageHeader
        title="Custom workflows"
        meta="Temporal workflows your teams write, run by their own workers; rvav starts them, shows their steps and asks for their approvals."
        actions={
          canEdit && (
            <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
              Register a workflow
            </Button>
          )
        }
      />
      <Panel title="Registered workflows" flush>
        {workflows.isPending && <SkeletonRows rows={3} cols={5} />}
        {workflows.isError && <ErrorState error={workflows.error} onRetry={() => void workflows.refetch()} />}
        {workflows.data && list.length === 0 && (
          <EmptyState
            title="No custom workflow"
            body="Register one to offer it in the catalog, on deployments or to subscriptions."
          />
        )}
        {list.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className={`${th} pl-4`}>
                    Workflow
                  </th>
                  <th scope="col" className={th}>
                    Type and queue
                  </th>
                  <th scope="col" className={th}>
                    Used as
                  </th>
                  <th scope="col" className={th}>
                    Running
                  </th>
                  <th scope="col" className={th}>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((w) => (
                  <tr key={w.key}>
                    <td className={`${td} pl-4`}>
                      <span className="font-medium">{w.title}</span>
                      {!w.enabled && (
                        <Badge tone="neutral" className="ml-2">
                          Off
                        </Badge>
                      )}
                      <div className="font-mono text-[11px] text-faint">{w.key}</div>
                      {w.used_by.length > 0 && <div className="text-xs text-muted">Used by {w.used_by.join(", ")}</div>}
                    </td>
                    <td className={`${td} font-mono text-xs`}>
                      {w.workflow_type}
                      <div className="text-faint">
                        {w.task_queue} · {days(w.timeout_seconds)}
                      </div>
                    </td>
                    <td className={`${td} text-xs`}>
                      {w.usages.map((u) => usageLabel[u]).join(", ")}
                      {w.day2?.offered && <div className="font-mono text-faint">wf.{w.key}</div>}
                    </td>
                    <td className={td}>{w.running}</td>
                    <td className={`${td} text-right whitespace-nowrap`}>
                      {canEdit && (
                        <>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Change ${w.title}`}
                            onClick={() => setEditing(w)}
                          >
                            Change
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Remove ${w.title}`}
                            onClick={() => window.confirm(`Remove ${w.title}?`) && remove.mutate(w.key ?? "")}
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
      {editing && <WorkflowDialog wf={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Page>
  );
}
