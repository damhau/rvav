// Custom resource types (ADR-0056): what rvav doesn't build itself, such as
// a load balancer, made and deleted by a custom workflow or an AWX template.
// Blueprints and composite steps use them; deployments show their resources.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Shapes } from "lucide-react";
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
import { Select, type Option } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm, q } from "@/lib/query";
import { errorAt, failed, fieldErrors, writable } from "../governance/common";

type S = components["schemas"];
type ResourceType = S["ResourceType"];
type Driver = S["ResourceTypeDriver"];
type Phase = "create" | "delete" | "update" | "read";

const phases: { phase: Phase; label: string; optional?: boolean }[] = [
  { phase: "create", label: "Creates it" },
  { phase: "delete", label: "Deletes it" },
  { phase: "update", label: "Updates it", optional: true },
  { phase: "read", label: "Reads it again", optional: true },
];

const kindLabel = { workflow: "Custom workflow", awx: "AWX template" } as const;

/** A step's driver as the table says it. */
const driverText = (d: Driver) => `${d.kind === "awx" ? "AWX" : "Workflow"} · ${d.name}`;

const sampleSchema = `{
  "type": "object",
  "properties": {
    "vip": { "type": "string", "title": "Virtual IP" }
  }
}`;

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** What runs one step: its kind, then the workflow or template, from those that can. */
function DriverField({
  phase,
  label,
  optional,
  value,
  onChange,
  workflows,
  templates,
  error,
}: {
  phase: Phase;
  label: string;
  optional?: boolean;
  value?: Driver;
  onChange: (d: Driver | undefined) => void;
  workflows: Option[];
  templates: Option[];
  error?: string;
}) {
  const kind = value?.kind ?? (optional ? "none" : "workflow");
  const offered = kind === "awx" ? templates : workflows;
  const options =
    value?.name && !offered.some((o) => o.value === value.name)
      ? [...offered, { value: value.name, label: value.name }]
      : offered;
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="text-secondary font-medium">{optional ? `${label} (optional)` : label}</legend>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[180px_minmax(0,1fr)]">
        <Field label="Run" htmlFor={`rt-${phase}-kind`}>
          <Select
            id={`rt-${phase}-kind`}
            value={kind}
            onChange={(k) =>
              onChange(
                k === "none" ? undefined : { kind: k as Driver["kind"], name: k === value?.kind ? value.name : "" },
              )
            }
            options={[
              ...(optional ? [{ value: "none", label: "Nothing" }] : []),
              { value: "workflow", label: kindLabel.workflow },
              { value: "awx", label: kindLabel.awx },
            ]}
          />
        </Field>
        {value && (
          <Field label={kind === "awx" ? "Job template" : "Workflow"} htmlFor={`rt-${phase}-name`} error={error}>
            <Select
              id={`rt-${phase}-name`}
              value={value.name}
              onChange={(name) => onChange({ ...value, name })}
              options={options}
              placeholder={kind === "awx" ? "Choose a job template…" : "Choose a workflow…"}
            />
          </Field>
        )}
      </div>
    </fieldset>
  );
}

function ResourceTypeSheet({ type, onClose }: { type?: ResourceType; onClose: () => void }) {
  const qc = useQueryClient();
  const workflows = useQuery(adm.workflows());
  const templates = useQuery(q.awxTemplates());
  const awxActions = useQuery(q.awxActions());
  const [f, setF] = useState({
    key: type?.key ?? "",
    title: type?.title ?? "",
    description: type?.description ?? "",
    enabled: type?.enabled ?? true,
  });
  const [drivers, setDrivers] = useState<Partial<Record<Phase, Driver>>>({
    create: type?.create ?? { kind: "workflow", name: "" },
    delete: type?.delete ?? { kind: "workflow", name: "" },
    ...(type?.update ? { update: type.update } : {}),
    ...(type?.read ? { read: type.read } : {}),
  });
  const [schema, setSchema] = useState(
    type?.property_schema ? JSON.stringify(type.property_schema, null, 2) : sampleSchema,
  );
  const [actions, setActions] = useState<string[]>(type?.actions ?? []);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const registered = workflows.data?.items ?? [];
  const workflowOptions = registered
    .filter((w) => w.usages.includes("resource_type"))
    .map((w) => ({ value: w.key ?? "", label: w.title, meta: w.key }));
  const templateOptions = (templates.data?.items ?? [])
    .filter((t) => !t.workflow && !t.gone)
    .map((t) => ({ value: t.name, label: t.name }));
  const offeredActions = [
    ...registered
      .filter((w) => w.usages.includes("day2_action"))
      .map((w) => ({ key: `wf.${w.key}`, label: w.day2?.name ?? w.title, what: "Custom workflow" })),
    ...(awxActions.data?.items ?? []).filter((a) => !a.gone).map((a) => ({ key: a.key, label: a.name, what: "AWX" })),
  ];
  const listedActions = [
    ...offeredActions,
    ...actions.filter((k) => !offeredActions.some((a) => a.key === k)).map((k) => ({ key: k, label: k, what: "" })),
  ];

  const save = useMutation({
    mutationFn: () => {
      let property_schema: Record<string, unknown>;
      try {
        property_schema = JSON.parse(schema) as Record<string, unknown>;
      } catch {
        return Promise.reject(new SyntaxError("property_schema"));
      }
      const body: S["ResourceTypeWrite"] = {
        ...(type ? {} : { key: f.key.trim() }),
        title: f.title,
        ...(f.description.trim() ? { description: f.description } : {}),
        create: drivers.create ?? { kind: "workflow", name: "" },
        delete: drivers.delete ?? { kind: "workflow", name: "" },
        ...(drivers.update ? { update: drivers.update } : {}),
        ...(drivers.read ? { read: drivers.read } : {}),
        property_schema,
        actions,
        enabled: f.enabled,
      };
      return type
        ? call(api.PUT("/admin/resource-types/{key}", { params: { path: { key: type.key ?? "" } }, body }))
        : call(api.POST("/admin/resource-types", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "resource-types"] });
      void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
      toast.success(`${f.title} saved`);
      onClose();
    },
    onError: (e) => {
      if (e instanceof SyntaxError) {
        setErrors({ "/property_schema": "This is not valid JSON." });
        return;
      }
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const actionError = (key: string) => {
    const i = actions.indexOf(key);
    return i < 0 ? undefined : errors[`/actions/${i}`];
  };
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={type ? `Change ${type.title}` : "New resource type"}
      description="Blueprints and composite steps make its resources; deployments show them like VMs, and deleting a deployment deletes them."
      className="w-[min(760px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save resource type"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[220px_1fr]">
        <Field
          label="Key"
          htmlFor="rt-key"
          error={errors["/key"]}
          hint={type ? "Set when defined" : "Its name in blueprints: load-balancer"}
        >
          <Input
            id="rt-key"
            className="font-mono"
            disabled={!!type}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label="Title" htmlFor="rt-title" error={errors["/title"]}>
          <Input id="rt-title" value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} />
        </Field>
      </div>
      <Field label="Description" htmlFor="rt-description">
        <Input
          id="rt-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">Its life</legend>
        <p className="text-secondary text-muted">
          A custom workflow registered for resource types gets the resource's inputs and returns its properties; an AWX
          template gets them as extra variables and returns them with set_stats.
        </p>
        {phases.map(({ phase, label, optional }) => (
          <DriverField
            key={phase}
            phase={phase}
            label={label}
            optional={optional}
            value={drivers[phase]}
            onChange={(d) => setDrivers({ ...drivers, [phase]: d })}
            workflows={workflowOptions}
            templates={templateOptions}
            error={errorAt(errors, `/${phase}`)}
          />
        ))}
      </fieldset>
      <Field
        label="Property schema (JSON Schema)"
        htmlFor="rt-schema"
        error={errorAt(errors, "/property_schema")}
        hint="What it returns: later steps read these properties, and the deployment page shows them by their titles."
      >
        <Textarea
          id="rt-schema"
          className="font-mono text-secondary"
          rows={8}
          value={schema}
          onChange={(e) => setSchema(e.target.value)}
        />
      </Field>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-secondary font-medium">Day-2 actions on its resources</legend>
        {listedActions.length === 0 ? (
          <p className="text-secondary text-muted">
            No custom workflow is registered as a day-2 action, and AWX offers none.
          </p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {listedActions.map((a) => (
              <li key={a.key}>
                <label htmlFor={`rt-action-${a.key}`} className="flex items-center gap-1.5 text-body">
                  <Checkbox
                    id={`rt-action-${a.key}`}
                    checked={actions.includes(a.key)}
                    onChange={(on) => setActions(on ? [...actions, a.key] : actions.filter((x) => x !== a.key))}
                  />
                  {a.label}
                  <span className="font-mono text-caption text-faint">{a.key}</span>
                </label>
                {actionError(a.key) && <p className="ml-6 text-secondary text-bad">{actionError(a.key)}</p>}
              </li>
            ))}
          </ul>
        )}
      </fieldset>
      <label htmlFor="rt-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="rt-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
        Items may use it
      </label>
    </Sheet>
  );
}

/** Why a type can't be removed, said in place of Remove; the server decides again. */
const cantRemove = (t: ResourceType) =>
  t.resources > 0
    ? `Remove: deployments have ${plural(t.resources, "resource")}`
    : t.used_by.length > 0
      ? "Remove: it is in use"
      : undefined;

const columns: Column<ResourceType>[] = [
  {
    id: "type",
    header: "Type",
    sortValue: (t) => t.title,
    cell: (t) => (
      <>
        <span className="font-medium">{t.title}</span>
        {t.enabled === false && (
          <Badge tone="neutral" className="ml-2">
            Off
          </Badge>
        )}
        <div className="font-mono text-caption text-faint">{t.key}</div>
        {t.used_by.length > 0 && <div className="text-secondary text-muted">Used by {t.used_by.join(", ")}</div>}
      </>
    ),
  },
  {
    id: "life",
    header: "Created and deleted by",
    cell: (t) => (
      <div className="font-mono text-secondary">
        {driverText(t.create)}
        <div>{driverText(t.delete)}</div>
        {t.update && <div className="text-faint">Update: {driverText(t.update)}</div>}
        {t.read && <div className="text-faint">Read: {driverText(t.read)}</div>}
      </div>
    ),
  },
  {
    id: "actions",
    header: "Day-2 actions",
    cell: (t) =>
      (t.actions ?? []).length > 0 ? (
        <div className="font-mono text-secondary">{(t.actions ?? []).join(", ")}</div>
      ) : (
        <span className="text-secondary text-faint">None</span>
      ),
  },
  { id: "resources", header: "Resources", align: "end", sortValue: (t) => t.resources, cell: (t) => t.resources },
];

export function ResourceTypesPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const types = useQuery(adm.resourceTypes());
  const [editing, setEditing] = useState<{ type?: ResourceType } | null>(null);
  const [removing, setRemoving] = useState<ResourceType | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "resource-types"] });
    void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
  };
  const toggle = useMutation({
    mutationFn: (t: ResourceType) =>
      call(
        api.PUT("/admin/resource-types/{key}", {
          params: { path: { key: t.key ?? "" } },
          body: {
            ...writable(t, "key", "resources", "used_by", "created_at", "updated_at"),
            enabled: t.enabled === false,
          },
        }),
      ),
    onSuccess: (t) => {
      refresh();
      toast.success(t.enabled ? `${t.title} is on` : `${t.title} is off`);
    },
    onError: (e) => failed(e, "Not saved"),
  });
  const remove = useMutation({
    mutationFn: (t: ResourceType) =>
      call(api.DELETE("/admin/resource-types/{key}", { params: { path: { key: t.key ?? "" } } })),
    onSuccess: (_, t) => {
      refresh();
      toast.success(`${t.title} removed`);
    },
    onError: (e) => {
      refresh();
      failed(e, "Not removed");
    },
  });
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing({})}>
      New resource type
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Resource types"
        description="What rvav doesn't build itself, such as load balancers: a custom workflow or an AWX template creates each resource, and another deletes it. Blueprints and composite steps use them."
        actions={types.data?.items.length ? create : undefined}
      />
      <Panel title="Defined types" flush>
        <DataTable
          label="Resource types"
          columns={columns}
          rows={types.data?.items}
          rowKey={(t) => t.key ?? t.title}
          minWidth={820}
          loading={types.isPending}
          error={types.isError && <ErrorState error={types.error} onRetry={() => void types.refetch()} />}
          empty={
            <EmptyState
              icon={Shapes}
              title="No resource type yet"
              body="Define one to let blueprints make what rvav doesn't build itself, with your workflows or AWX templates."
              action={create || undefined}
            />
          }
          rowMenuLabel={(t) => `Actions for ${t.title}`}
          rowMenu={
            canEdit
              ? (t) => (
                  <>
                    <MenuItem onSelect={() => setEditing({ type: t })}>Change</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(t)}>
                      {t.enabled === false ? "Turn on" : "Turn off"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={Boolean(cantRemove(t))} onSelect={() => setRemoving(t)}>
                      {cantRemove(t) ?? "Remove"}
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      {editing && <ResourceTypeSheet type={editing.type} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.title}?`}
          description="Blueprints and composite steps can no longer use it."
          consequences="rvav forgets the type. Its custom workflows and AWX templates aren't changed."
          confirmLabel="Remove resource type"
          pending={remove.isPending}
          onConfirm={() => {
            const t = removing;
            setRemoving(null);
            remove.mutate(t);
          }}
        />
      )}
    </Page>
  );
}
