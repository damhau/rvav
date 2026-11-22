// Custom resource types (ADR-0056): what rvav doesn't build itself, such as
// a load balancer, made and deleted by a custom workflow or an AWX template.
// Blueprints and composite steps use them; deployments show their resources.
import type { TFunction } from "i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Shapes } from "lucide-react";
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
import { Select, type Option } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm, keys, q } from "@/lib/query";
import { errorAt, failed, fieldErrors, writable } from "../governance/common";

type S = components["schemas"];
type ResourceType = S["ResourceType"];
type Driver = S["ResourceTypeDriver"];
type Phase = "create" | "delete" | "update" | "read";

const tx = i18n.getFixedT(null, "admin-extensibility");

const phases: { phase: Phase; optional?: boolean }[] = [
  { phase: "create" },
  { phase: "delete" },
  { phase: "update", optional: true },
  { phase: "read", optional: true },
];

/** A step's driver as the table says it. */
const driverText = (d: Driver) => tx(`resourceTypes.driver.${d.kind}`, { name: d.name });

const sampleSchema = `{
  "type": "object",
  "properties": {
    "vip": { "type": "string", "title": "Virtual IP" }
  }
}`;

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
  const { t } = useTranslation("admin-extensibility");
  const kind = value?.kind ?? (optional ? "none" : "workflow");
  const offered = kind === "awx" ? templates : workflows;
  const options =
    value?.name && !offered.some((o) => o.value === value.name)
      ? [...offered, { value: value.name, label: value.name }]
      : offered;
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="text-secondary font-medium">
        {optional ? t("resourceTypes.optional", { label }) : label}
      </legend>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[180px_minmax(0,1fr)]">
        <Field label={t("resourceTypes.run")} htmlFor={`rt-${phase}-kind`}>
          <Select
            id={`rt-${phase}-kind`}
            value={kind}
            onChange={(k) =>
              onChange(
                k === "none" ? undefined : { kind: k as Driver["kind"], name: k === value?.kind ? value.name : "" },
              )
            }
            options={[
              ...(optional ? [{ value: "none", label: t("resourceTypes.nothing") }] : []),
              { value: "workflow", label: t("target.kinds.workflow") },
              { value: "awx", label: t("target.kinds.awx") },
            ]}
          />
        </Field>
        {value && (
          <Field
            label={kind === "awx" ? t("resourceTypes.jobTemplate") : t("resourceTypes.workflow")}
            htmlFor={`rt-${phase}-name`}
            error={error}
          >
            <Select
              id={`rt-${phase}-name`}
              value={value.name}
              onChange={(name) => onChange({ ...value, name })}
              options={options}
              placeholder={kind === "awx" ? t("resourceTypes.chooseTemplate") : t("resourceTypes.chooseWorkflow")}
            />
          </Field>
        )}
      </div>
    </fieldset>
  );
}

function ResourceTypeSheet({ type, onClose }: { type?: ResourceType; onClose: () => void }) {
  const { t } = useTranslation("admin-extensibility");
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
      .map((w) => ({ key: `wf.${w.key}`, label: w.day2?.name ?? w.title })),
    ...(awxActions.data?.items ?? []).filter((a) => !a.gone).map((a) => ({ key: a.key, label: a.name })),
  ];
  const listedActions = [
    ...offeredActions,
    ...actions.filter((k) => !offeredActions.some((a) => a.key === k)).map((k) => ({ key: k, label: k })),
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
      // What the composite designer offers to steps is made from them.
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.compositeSteps });
      toast.success(t("shared.saved", { name: f.title }));
      onClose();
    },
    onError: (e) => {
      if (e instanceof SyntaxError) {
        setErrors({ "/property_schema": t("shared.invalidJson") });
        return;
      }
      setErrors(fieldErrors(e));
      failed(e, t("shared.notSaved"));
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
      title={type ? t("shared.changeName", { name: type.title }) : t("resourceTypes.sheet.new")}
      description={t("resourceTypes.sheet.lead")}
      className="w-[min(760px,100vw)]"
      submitLabel={save.isPending ? t("shared.saving") : t("resourceTypes.sheet.save")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[220px_1fr]">
        <Field
          label={t("shared.key")}
          htmlFor="rt-key"
          error={errors["/key"]}
          hint={type ? t("resourceTypes.sheet.keySet") : t("resourceTypes.sheet.keyHint")}
        >
          <Input
            id="rt-key"
            className="font-mono"
            disabled={!!type}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label={t("shared.title")} htmlFor="rt-title" error={errors["/title"]}>
          <Input id="rt-title" value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} />
        </Field>
      </div>
      <Field label={t("shared.description")} htmlFor="rt-description">
        <Input
          id="rt-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">{t("resourceTypes.sheet.life")}</legend>
        <p className="text-secondary text-muted">{t("resourceTypes.sheet.lifeLead")}</p>
        {phases.map(({ phase, optional }) => (
          <DriverField
            key={phase}
            phase={phase}
            label={t(`resourceTypes.phases.${phase}`)}
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
        label={t("resourceTypes.sheet.schema")}
        htmlFor="rt-schema"
        error={errorAt(errors, "/property_schema")}
        hint={t("resourceTypes.sheet.schemaHint")}
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
        <legend className="text-secondary font-medium">{t("resourceTypes.sheet.actions")}</legend>
        {listedActions.length === 0 ? (
          <p className="text-secondary text-muted">{t("resourceTypes.sheet.noActions")}</p>
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
        {t("resourceTypes.sheet.enabled")}
      </label>
    </Sheet>
  );
}

/** Why a type can't be removed, said in place of Remove; the server decides again. */
const cantRemove = (rt: ResourceType) =>
  rt.resources > 0
    ? tx("resourceTypes.cantRemove", { count: rt.resources })
    : rt.used_by.length > 0
      ? tx("shared.inUse")
      : undefined;

const columnsFor = (t: TFunction<"admin-extensibility">): Column<ResourceType>[] => [
  {
    id: "type",
    header: t("resourceTypes.columns.type"),
    sortValue: (rt) => rt.title,
    cell: (rt) => (
      <>
        <span className="font-medium">{rt.title}</span>
        {rt.enabled === false && (
          <Badge tone="neutral" className="ml-2">
            {t("shared.off")}
          </Badge>
        )}
        <div className="font-mono text-caption text-faint">{rt.key}</div>
        {rt.used_by.length > 0 && (
          <div className="text-secondary text-muted">{t("shared.usedBy", { names: rt.used_by.join(", ") })}</div>
        )}
      </>
    ),
  },
  {
    id: "life",
    header: t("resourceTypes.columns.life"),
    cell: (rt) => (
      <div className="font-mono text-secondary">
        {driverText(rt.create)}
        <div>{driverText(rt.delete)}</div>
        {rt.update && (
          <div className="text-faint">{t("resourceTypes.columns.update", { driver: driverText(rt.update) })}</div>
        )}
        {rt.read && (
          <div className="text-faint">{t("resourceTypes.columns.read", { driver: driverText(rt.read) })}</div>
        )}
      </div>
    ),
  },
  {
    id: "actions",
    header: t("resourceTypes.columns.actions"),
    cell: (rt) =>
      (rt.actions ?? []).length > 0 ? (
        <div className="font-mono text-secondary">{(rt.actions ?? []).join(", ")}</div>
      ) : (
        <span className="text-secondary text-faint">{t("shared.none")}</span>
      ),
  },
  {
    id: "resources",
    header: t("resourceTypes.columns.resources"),
    align: "end",
    sortValue: (rt) => rt.resources,
    cell: (rt) => rt.resources,
  },
];

export function ResourceTypesPage() {
  const { t } = useTranslation(["admin-extensibility", "shell"]);
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const types = useQuery(adm.resourceTypes());
  const [editing, setEditing] = useState<{ type?: ResourceType } | null>(null);
  const [removing, setRemoving] = useState<ResourceType | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "resource-types"] });
    void qc.invalidateQueries({ queryKey: ["admin", "workflows"] });
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.compositeSteps });
  };
  const toggle = useMutation({
    mutationFn: (rt: ResourceType) =>
      call(
        api.PUT("/admin/resource-types/{key}", {
          params: { path: { key: rt.key ?? "" } },
          body: {
            ...writable(rt, "key", "resources", "used_by", "created_at", "updated_at"),
            enabled: rt.enabled === false,
          },
        }),
      ),
    onSuccess: (rt) => {
      refresh();
      toast.success(rt.enabled ? t("shared.isOn", { name: rt.title }) : t("shared.isOff", { name: rt.title }));
    },
    onError: (e) => failed(e, t("shared.notSaved")),
  });
  const remove = useMutation({
    mutationFn: (rt: ResourceType) =>
      call(api.DELETE("/admin/resource-types/{key}", { params: { path: { key: rt.key ?? "" } } })),
    onSuccess: (_, rt) => {
      refresh();
      toast.success(t("shared.removed", { name: rt.title }));
    },
    onError: (e) => {
      refresh();
      failed(e, t("shared.notRemoved"));
    },
  });
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing({})}>
      {t("resourceTypes.sheet.new")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.resourceTypes")}
        description={t("resourceTypes.lead")}
        actions={types.data?.items.length ? create : undefined}
      />
      <Panel title={t("resourceTypes.defined")} flush>
        <DataTable
          label={t("resourceTypes.label")}
          columns={columnsFor(t)}
          rows={types.data?.items}
          rowKey={(rt) => rt.key ?? rt.title}
          minWidth={820}
          loading={types.isPending}
          error={types.isError && <ErrorState error={types.error} onRetry={() => void types.refetch()} />}
          empty={
            <EmptyState
              icon={Shapes}
              title={t("resourceTypes.none")}
              body={t("resourceTypes.noneBody")}
              action={create || undefined}
            />
          }
          rowMenuLabel={(rt) => t("shared.actionsFor", { name: rt.title })}
          rowMenu={
            canEdit
              ? (rt) => (
                  <>
                    <MenuItem onSelect={() => setEditing({ type: rt })}>{t("shared.change")}</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(rt)}>
                      {rt.enabled === false ? t("shared.turnOn") : t("shared.turnOff")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={Boolean(cantRemove(rt))} onSelect={() => setRemoving(rt)}>
                      {cantRemove(rt) ?? t("shared.remove")}
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
          title={t("shared.removeName", { name: removing.title })}
          description={t("resourceTypes.remove.body")}
          consequences={t("resourceTypes.remove.consequences")}
          confirmLabel={t("resourceTypes.remove.confirm")}
          pending={remove.isPending}
          onConfirm={() => {
            const rt = removing;
            setRemoving(null);
            remove.mutate(rt);
          }}
        />
      )}
    </Page>
  );
}
