// Custom Temporal workflows (S10, ADR-0022): registered here, they run as
// catalog items, day-2 actions, subscription targets or composite steps.
import type { TFunction } from "i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Workflow } from "lucide-react";
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
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { formatList, formatNumber } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, writable } from "../governance/common";

type S = components["schemas"];
type Registration = S["WorkflowRegistration"];
type Usage = S["WorkflowUsage"];
type Day2Type = S["WorkflowDay2"]["types"][number];

const tx = i18n.getFixedT(null, "admin-extensibility");

const usages: Usage[] = ["catalog_item", "day2_action", "subscription_target", "composite_step", "resource_type"];
const usageLabel = (u: Usage) => tx(`workflows.usage.${u}`);

const days = (seconds: number) => {
  if (seconds < 86400) return tx("workflows.hours", { hours: Math.round(seconds / 3600) });
  const d = Math.round((seconds / 86400) * 10) / 10;
  return tx("workflows.days", { count: d, days: formatNumber(d) });
};

const anyKey = "<key>";
const actionKey = (key?: string) => `wf.${key ?? ""}`;

const sampleSchema = `{
  "type": "object",
  "required": ["application"],
  "properties": {
    "application": { "type": "string", "title": "Application" }
  }
}`;

function WorkflowSheet({ wf, onClose }: { wf?: Registration; onClose: () => void }) {
  const { t } = useTranslation("admin-extensibility");
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
  const [used, setUsed] = useState<Usage[]>(wf?.usages ?? ["catalog_item"]);
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
        usages: used,
        day2: used.includes("day2_action") ? day2 : undefined,
        enabled: f.enabled,
      };
      return wf
        ? call(api.PUT("/admin/workflows/{key}", { params: { path: { key: wf.key ?? "" } }, body }))
        : call(api.POST("/admin/workflows", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
      toast.success(t(wf ? "shared.saved" : "shared.added", { name: f.title }));
      onClose();
    },
    onError: (e) => {
      if (e instanceof SyntaxError) {
        setErrors({ "/input_schema": t("shared.invalidJson") });
        return;
      }
      setErrors(fieldErrors(e));
      failed(e, t("shared.notSaved"));
    },
  });
  const toggle = <T,>(list: T[], v: T, on: boolean) => (on ? [...list, v] : list.filter((x) => x !== v));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={wf ? t("shared.changeName", { name: wf.title }) : t("workflows.sheet.add")}
      description={t("workflows.sheet.lead")}
      className="w-[min(760px,100vw)]"
      submitLabel={save.isPending ? t("shared.saving") : t(wf ? "workflows.sheet.save" : "workflows.sheet.addSubmit")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[200px_1fr]">
        <Field
          label={t("shared.key")}
          htmlFor="wf-key"
          error={errors["/key"]}
          hint={wf ? t("workflows.sheet.keySet") : t("workflows.sheet.keyHint")}
        >
          <Input
            id="wf-key"
            className="font-mono"
            disabled={!!wf}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label={t("shared.title")} htmlFor="wf-title" error={errors["/title"]}>
          <Input id="wf-title" value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} />
        </Field>
      </div>
      <Field label={t("shared.description")} htmlFor="wf-description">
        <Input
          id="wf-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr_140px]">
        <Field
          label={t("workflows.sheet.type")}
          htmlFor="wf-type"
          error={errors["/workflow_type"]}
          hint={t("workflows.sheet.typeHint")}
        >
          <Input
            id="wf-type"
            className="font-mono"
            value={f.workflow_type}
            onChange={(e) => setF({ ...f, workflow_type: e.target.value })}
          />
        </Field>
        <Field label={t("workflows.sheet.queue")} htmlFor="wf-queue" error={errors["/task_queue"]}>
          <Input
            id="wf-queue"
            className="font-mono"
            value={f.task_queue}
            onChange={(e) => setF({ ...f, task_queue: e.target.value })}
          />
        </Field>
        <Field label={t("workflows.sheet.timeout")} htmlFor="wf-timeout" error={errors["/timeout_seconds"]}>
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
        label={t("workflows.sheet.schema")}
        htmlFor="wf-schema"
        error={errorAt(errors, "/input_schema")}
        hint={t("workflows.sheet.schemaHint")}
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
        <legend className="text-secondary font-medium">{t("workflows.sheet.usedAs")}</legend>
        <div className="flex flex-wrap gap-4">
          {usages.map((u) => (
            <label key={u} htmlFor={`wf-usage-${u}`} className="flex items-center gap-1.5 text-body">
              <Checkbox
                id={`wf-usage-${u}`}
                checked={used.includes(u)}
                onChange={(on) => setUsed(toggle(used, u, on))}
              />
              {t(`workflows.usage.${u}`)}
            </label>
          ))}
        </div>
        {errors["/usages"] && <p className="text-secondary text-bad">{errors["/usages"]}</p>}
      </fieldset>
      {used.includes("day2_action") && (
        <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
          <legend className="px-1 text-secondary font-medium">
            {t("workflows.sheet.day2As", { key: f.key || anyKey })}
          </legend>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr]">
            <Field label={t("workflows.sheet.actionName")} htmlFor="wf-day2-name" error={errors["/day2/name"]}>
              <Input id="wf-day2-name" value={day2.name} onChange={(e) => setDay2({ ...day2, name: e.target.value })} />
            </Field>
            <fieldset className="flex flex-col gap-1.5">
              <legend className="text-secondary font-medium">{t("workflows.sheet.runsOn")}</legend>
              <div className="flex gap-4">
                {(["vsphere.vm", "deployment"] as Day2Type[]).map((type) => (
                  <label key={type} htmlFor={`wf-day2-${type}`} className="flex items-center gap-1.5 text-body">
                    <Checkbox
                      id={`wf-day2-${type}`}
                      checked={day2.types.includes(type)}
                      onChange={(on) => setDay2({ ...day2, types: toggle(day2.types, type, on) })}
                    />
                    {type === "vsphere.vm" ? t("workflows.sheet.vms") : t("workflows.sheet.deployments")}
                  </label>
                ))}
              </div>
              {errors["/day2/types"] && <p className="text-secondary text-bad">{errors["/day2/types"]}</p>}
            </fieldset>
          </div>
          <Field
            label={t("workflows.sheet.offeredWhen")}
            htmlFor="wf-day2-condition"
            error={errors["/day2/condition"]}
            hint={t("workflows.sheet.offeredHint")}
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
            {t("workflows.sheet.offer")}
          </label>
        </fieldset>
      )}
      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-secondary font-medium">{t("workflows.sheet.projects")}</legend>
        <p className="text-secondary text-muted">{t("workflows.sheet.projectsLead")}</p>
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
        {t("shared.enabled")}
      </label>
    </Sheet>
  );
}

/** Why a workflow can't be removed, said in place of Remove; the server also refuses one requests ran. */
const cantRemove = (w: Registration) =>
  w.running > 0 ? tx("workflows.cantRemove") : w.used_by.length > 0 ? tx("shared.inUse") : undefined;

const columnsFor = (t: TFunction<"admin-extensibility">): Column<Registration>[] => [
  {
    id: "workflow",
    header: t("workflows.columns.workflow"),
    sortValue: (w) => w.title,
    cell: (w) => (
      <>
        <span className="font-medium">{w.title}</span>
        {!w.enabled && (
          <Badge tone="neutral" className="ml-2">
            {t("shared.off")}
          </Badge>
        )}
        <div className="font-mono text-caption text-faint">{w.key}</div>
        {w.used_by.length > 0 && (
          <div className="text-secondary text-muted">{t("shared.usedBy", { names: w.used_by.join(", ") })}</div>
        )}
      </>
    ),
  },
  {
    id: "type",
    header: t("workflows.columns.type"),
    sortValue: (w) => w.workflow_type,
    cell: (w) => (
      <div className="font-mono text-secondary">
        {w.workflow_type}
        <div className="text-faint">
          {t("workflows.columns.queue", { queue: w.task_queue, timeout: days(w.timeout_seconds) })}
        </div>
      </div>
    ),
  },
  {
    id: "usages",
    header: t("workflows.sheet.usedAs"),
    cell: (w) => (
      <div className="text-secondary">
        {w.usages.map(usageLabel).join(", ")}
        {w.day2?.offered && <div className="font-mono text-faint">{actionKey(w.key)}</div>}
      </div>
    ),
  },
  {
    id: "running",
    header: t("workflows.columns.running"),
    align: "end",
    sortValue: (w) => w.running,
    cell: (w) => w.running,
  },
];

export function WorkflowsPage() {
  const { t } = useTranslation(["admin-extensibility", "shell"]);
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
      toast.success(w.enabled ? t("shared.isOn", { name: w.title }) : t("shared.isOff", { name: w.title }));
    },
    onError: (e) => failed(e, t("shared.notSaved")),
  });
  const remove = useMutation({
    mutationFn: (w: Registration) =>
      call(api.DELETE("/admin/workflows/{key}", { params: { path: { key: w.key ?? "" } } })),
    onSuccess: (_, w) => {
      refresh();
      toast.success(t("shared.removed", { name: w.title }));
    },
    onError: (e) => failed(e, t("shared.notRemoved")),
  });
  const register = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      {t("workflows.sheet.add")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.workflows")}
        description={t("workflows.lead")}
        actions={workflows.data?.items.length ? register : undefined}
      />
      <Panel title={t("workflows.registered")} flush>
        <DataTable
          label={t("workflows.registered")}
          columns={columnsFor(t)}
          rows={workflows.data?.items}
          rowKey={(w) => w.key ?? w.title}
          minWidth={860}
          loading={workflows.isPending}
          error={workflows.isError && <ErrorState error={workflows.error} onRetry={() => void workflows.refetch()} />}
          empty={
            <EmptyState
              icon={Workflow}
              title={t("workflows.none")}
              body={t("workflows.noneBody")}
              action={register || undefined}
            />
          }
          rowMenuLabel={(w) => t("shared.actionsFor", { name: w.title })}
          rowMenu={
            canEdit
              ? (w) => (
                  <>
                    <MenuItem onSelect={() => setEditing(w)}>{t("shared.change")}</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(w)}>
                      {w.enabled ? t("shared.turnOff") : t("shared.turnOn")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={Boolean(cantRemove(w))} onSelect={() => setRemoving(w)}>
                      {cantRemove(w) ?? t("shared.remove")}
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
          title={t("shared.removeName", { name: removing.title })}
          description={t("workflows.remove.body")}
          consequences={t("workflows.remove.consequences", {
            usages: formatList(
              removing.usages.map((u) => usageLabel(u).toLowerCase()),
              "disjunction",
            ),
          })}
          confirmLabel={t("workflows.remove.confirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Page>
  );
}
