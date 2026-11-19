// Custom Temporal workflows (S10, ADR-0022): registered here, they run as
// catalog items, day-2 actions, subscription targets or composite steps.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Workflow } from "lucide-react";
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
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, writable } from "../governance/common";

type S = components["schemas"];
type Registration = S["WorkflowRegistration"];
type Usage = S["WorkflowUsage"];
type Day2Type = S["WorkflowDay2"]["types"][number];

export const usageLabel: Record<Usage, string> = {
  catalog_item: "Catalog item",
  day2_action: "Day-2 action",
  subscription_target: "Subscription target",
  composite_step: "Composite step",
  resource_type: "Resource type",
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

function WorkflowSheet({ wf, onClose }: { wf?: Registration; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useQuery(adm.projectNames());
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
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={wf ? `Change ${wf.title}` : "Register a workflow"}
      description="Its own workers run it in Temporal; rvav starts it with the form its input schema describes."
      className="w-[min(760px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save workflow"}
      onSubmit={() => {
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
          className="font-mono text-secondary"
          rows={8}
          value={schema}
          onChange={(e) => setSchema(e.target.value)}
        />
      </Field>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-secondary font-medium">Used as</legend>
        <div className="flex flex-wrap gap-4">
          {(Object.keys(usageLabel) as Usage[]).map((u) => (
            <label key={u} htmlFor={`wf-usage-${u}`} className="flex items-center gap-1.5 text-body">
              <Checkbox
                id={`wf-usage-${u}`}
                checked={usages.includes(u)}
                onChange={(on) => setUsages(toggle(usages, u, on))}
              />
              {usageLabel[u]}
            </label>
          ))}
        </div>
        {errors["/usages"] && <p className="text-secondary text-bad">{errors["/usages"]}</p>}
      </fieldset>
      {usages.includes("day2_action") && (
        <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
          <legend className="px-1 text-secondary font-medium">As the day-2 action wf.{f.key || "<key>"}</legend>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr]">
            <Field label="Action name" htmlFor="wf-day2-name" error={errors["/day2/name"]}>
              <Input id="wf-day2-name" value={day2.name} onChange={(e) => setDay2({ ...day2, name: e.target.value })} />
            </Field>
            <fieldset className="flex flex-col gap-1.5">
              <legend className="text-secondary font-medium">Runs on</legend>
              <div className="flex gap-4">
                {(["vsphere.vm", "deployment"] as Day2Type[]).map((t) => (
                  <label key={t} htmlFor={`wf-day2-${t}`} className="flex items-center gap-1.5 text-body">
                    <Checkbox
                      id={`wf-day2-${t}`}
                      checked={day2.types.includes(t)}
                      onChange={(on) => setDay2({ ...day2, types: toggle(day2.types, t, on) })}
                    />
                    {t === "vsphere.vm" ? "VMs" : "Deployments"}
                  </label>
                ))}
              </div>
              {errors["/day2/types"] && <p className="text-secondary text-bad">{errors["/day2/types"]}</p>}
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
              className="font-mono text-secondary"
              value={day2.condition ?? ""}
              onChange={(e) => setDay2({ ...day2, condition: e.target.value })}
            />
          </Field>
          <label htmlFor="wf-day2-offered" className="flex items-center gap-1.5 text-body">
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
        <legend className="text-secondary font-medium">Projects</legend>
        <p className="text-secondary text-muted">None ticked: every project may use it.</p>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {(projects.data?.items ?? []).map((p) => (
            <label key={p.id} htmlFor={`wf-project-${p.id}`} className="flex items-center gap-1.5 text-body">
              <Checkbox
                id={`wf-project-${p.id}`}
                checked={projectIds.includes(p.id)}
                onChange={(on) => setProjectIds(toggle(projectIds, p.id, on))}
              />
              {p.name}
            </label>
          ))}
        </div>
        {errorAt(errors, "/project_ids") && (
          <p className="text-secondary text-bad">{errorAt(errors, "/project_ids")}</p>
        )}
      </fieldset>
      <label htmlFor="wf-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="wf-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
        Enabled
      </label>
    </Sheet>
  );
}

/** Why a workflow can't be removed, said in place of Remove; the server also refuses one requests ran. */
const cantRemove = (w: Registration) =>
  w.running > 0 ? "Remove: runs haven't ended" : w.used_by.length > 0 ? "Remove: it is in use" : undefined;

const columns: Column<Registration>[] = [
  {
    id: "workflow",
    header: "Workflow",
    sortValue: (w) => w.title,
    cell: (w) => (
      <>
        <span className="font-medium">{w.title}</span>
        {!w.enabled && (
          <Badge tone="neutral" className="ml-2">
            Off
          </Badge>
        )}
        <div className="font-mono text-caption text-faint">{w.key}</div>
        {w.used_by.length > 0 && <div className="text-secondary text-muted">Used by {w.used_by.join(", ")}</div>}
      </>
    ),
  },
  {
    id: "type",
    header: "Type and queue",
    sortValue: (w) => w.workflow_type,
    cell: (w) => (
      <div className="font-mono text-secondary">
        {w.workflow_type}
        <div className="text-faint">
          {w.task_queue}, {days(w.timeout_seconds)}
        </div>
      </div>
    ),
  },
  {
    id: "usages",
    header: "Used as",
    cell: (w) => (
      <div className="text-secondary">
        {w.usages.map((u) => usageLabel[u]).join(", ")}
        {w.day2?.offered && <div className="font-mono text-faint">wf.{w.key}</div>}
      </div>
    ),
  },
  { id: "running", header: "Running", align: "end", sortValue: (w) => w.running, cell: (w) => w.running },
];

export function WorkflowsPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const workflows = useQuery(adm.workflows());
  const [editing, setEditing] = useState<Registration | "new" | null>(null);
  const [removing, setRemoving] = useState<Registration | null>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
  const toggle = useMutation({
    mutationFn: (w: Registration) =>
      call(
        api.PUT("/admin/workflows/{key}", {
          params: { path: { key: w.key ?? "" } },
          body: { ...writable(w, "running", "used_by", "created_at", "updated_at"), enabled: !w.enabled },
        }),
      ),
    onSuccess: (w) => {
      refresh();
      toast.success(w.enabled ? `${w.title} is on` : `${w.title} is off`);
    },
    onError: (e) => failed(e, "Not saved"),
  });
  const remove = useMutation({
    mutationFn: (w: Registration) =>
      call(api.DELETE("/admin/workflows/{key}", { params: { path: { key: w.key ?? "" } } })),
    onSuccess: (_, w) => {
      refresh();
      toast.success(`${w.title} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const register = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      Register a workflow
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Custom workflows"
        description="Temporal workflows your teams write, run by their own workers; rvav starts them, shows their steps and asks for their approvals."
        actions={workflows.data?.items.length ? register : undefined}
      />
      <Panel title="Registered workflows" flush>
        <DataTable
          label="Registered workflows"
          columns={columns}
          rows={workflows.data?.items}
          rowKey={(w) => w.key ?? w.title}
          minWidth={860}
          loading={workflows.isPending}
          error={workflows.isError && <ErrorState error={workflows.error} onRetry={() => void workflows.refetch()} />}
          empty={
            <EmptyState
              icon={Workflow}
              title="No custom workflow yet"
              body="Register one to offer it in the catalog, on deployments or to subscriptions."
              action={register || undefined}
            />
          }
          rowMenuLabel={(w) => `Actions for ${w.title}`}
          rowMenu={
            canEdit
              ? (w) => (
                  <>
                    <MenuItem onSelect={() => setEditing(w)}>Change</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(w)}>
                      {w.enabled ? "Turn off" : "Turn on"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={Boolean(cantRemove(w))} onSelect={() => setRemoving(w)}>
                      {cantRemove(w) ?? "Remove"}
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      {editing && <WorkflowSheet wf={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.title}?`}
          description="rvav forgets its registration: it no longer starts it. Its workers and Temporal aren't changed."
          consequences={`It is no longer offered as ${removing.usages
            .map((u) => usageLabel[u].toLowerCase())
            .join(", ")}. If requests ran it, it is refused: turn it off instead.`}
          confirmLabel="Remove workflow"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Page>
  );
}
