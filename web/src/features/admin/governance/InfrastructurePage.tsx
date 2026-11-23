// VM templates, size presets and network mappings (S9, ADR-0020): what
// blueprints' template and size pickers offer, and the subnet each vCenter
// network deploys into; rvav's IP ranges (ADR-0063); and the vCenter
// objects' tags (ADR-0035).
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Cpu, HardDrive, Network, Server, X } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { FilterBar, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useEnvName } from "@/lib/environments";
import { adm } from "@/lib/query";
import { can } from "@/lib/can";
import { formatList, formatNumber } from "@/lib/format";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, OfferEditor, offersText, writable } from "./common";
import { IPRanges } from "./IPRanges";

type S = components["schemas"];

function useProjectName() {
  const projects = useQuery(adm.projectNames());
  return (id: string) => projects.data?.items.find((p) => p.id === id)?.name ?? id;
}

type Source = S["VmTemplateSourceWrite"];

// A select's options can't be empty: the vCenter integration's without a name.
const unnamed = "(integration)";

/** A catalogue template's source in each vCenter (ADR-0037). */
function SourceEditor({
  value,
  onChange,
  errors,
  stored,
}: {
  value: Source[];
  onChange: (v: Source[]) => void;
  errors: Record<string, string>;
  /** The sources as saved, which say whether vCenter has them now. */
  stored?: S["VmTemplateSource"][];
}) {
  const { t } = useTranslation("admin-governance");
  const vcenters = useQuery(adm.vcenters());
  const primary = vcenters.data?.items.find((v) => v.primary)?.name ?? "";
  const options = (vcenters.data?.items ?? []).map((v) => ({
    value: v.name || unnamed,
    label: v.name || t("infra.source.integrationName"),
    meta: v.primary ? t("infra.source.integration") : undefined,
  }));
  const valueOf = (src: Source) => src.vcenter || primary || unnamed;
  for (const src of value) {
    if (!options.some((o) => o.value === valueOf(src)))
      options.push({ value: valueOf(src), label: src.vcenter || t("infra.source.integrationName"), meta: undefined });
  }
  const set = (i: number, src: Source) => onChange(value.map((x, j) => (j === i ? src : x)));
  const unused = options.find((o) => !value.some((src) => valueOf(src) === o.value));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">{t("infra.source.legend")}</legend>
      <p className="text-secondary text-muted">{t("infra.source.lead")}</p>
      {errors["/sources"] && <p className="text-secondary text-bad">{errors["/sources"]}</p>}
      {value.map((src, i) => {
        const at = `/sources/${i}`;
        const missing =
          stored?.find(
            (x) =>
              (x.vcenter || unnamed) === valueOf(src) &&
              x.template === src.template &&
              x.content_library === (src.content_library ?? ""),
          )?.in_inventory === false;
        return (
          <div
            key={i}
            className="flex flex-col gap-2 rounded-md border border-line p-3"
            role="group"
            aria-label={t("infra.source.group", { n: i + 1 })}
          >
            <div className="flex items-end gap-2">
              <Field
                label={t("infra.source.vcenter")}
                htmlFor={`tpl-src-${i}-vc`}
                error={errors[`${at}/vcenter`]}
                className="flex-1"
              >
                <Select
                  id={`tpl-src-${i}-vc`}
                  value={valueOf(src)}
                  onChange={(v) => set(i, { ...src, vcenter: v === unnamed ? "" : v })}
                  options={options}
                  loading={vcenters.isPending}
                />
              </Field>
              {missing && (
                <Badge tone="warn" className="mb-2">
                  {t("infra.source.notFound")}
                </Badge>
              )}
              <Button
                type="button"
                size="icon"
                variant="quiet"
                aria-label={t("infra.source.remove", { n: i + 1 })}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" />
              </Button>
            </div>
            <div className="grid grid-cols-[1fr_160px] gap-2">
              <Field label={t("infra.source.template")} htmlFor={`tpl-src-${i}-tpl`} error={errors[`${at}/template`]}>
                <Input
                  id={`tpl-src-${i}-tpl`}
                  className="font-mono"
                  value={src.template}
                  onChange={(e) => set(i, { ...src, template: e.target.value })}
                />
              </Field>
              <Field
                label={t("infra.source.library")}
                htmlFor={`tpl-src-${i}-lib`}
                hint={t("infra.source.libraryHint")}
              >
                <Input
                  id={`tpl-src-${i}-lib`}
                  className="font-mono"
                  value={src.content_library ?? ""}
                  onChange={(e) => set(i, { ...src, content_library: e.target.value })}
                />
              </Field>
            </div>
          </div>
        );
      })}
      {unused && (
        <div>
          <Button
            type="button"
            size="sm"
            variant="quiet"
            onClick={() =>
              onChange([
                ...value,
                { vcenter: unused.value === unnamed ? "" : unused.value, template: "", content_library: "" },
              ])
            }
          >
            {t("infra.source.add")}
          </Button>
        </div>
      )}
    </fieldset>
  );
}

type ConsKind = keyof S["TemplateConstraints"];
const kinds: ConsKind[] = ["compute", "storage", "network"];
// Product and directive names read the same in every language.
const userDataFirstLine = { linux: "#cloud-config", windows: "#ps1_sysnative" } as const;
const cloudInit = "cloud-init";
const cloudbaseInit = "cloudbase-init";

const tagList = (text: string) =>
  text
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);

/** A template's own constraint tags (ADR-0035), typed comma-separated by kind. */
function ConstraintFields({
  value,
  onChange,
  errors,
}: {
  value: Record<ConsKind, string>;
  onChange: (v: Record<ConsKind, string>) => void;
  errors: Record<string, string>;
}) {
  const { t } = useTranslation("admin-governance");
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">{t("infra.constraints.legend")}</legend>
      <p className="text-secondary text-muted">
        <Trans t={t} i18nKey="infra.constraints.lead" components={{ mono: <span className="font-mono" /> }} />
      </p>
      <div className="grid grid-cols-3 gap-2">
        {kinds.map((kind) => (
          <Field
            key={kind}
            label={t(`infra.constraints.kind.${kind}`)}
            htmlFor={`tpl-cons-${kind}`}
            error={errorAt(errors, `/constraints/${kind}`)}
          >
            <Input
              id={`tpl-cons-${kind}`}
              className="font-mono"
              value={value[kind]}
              onChange={(e) => onChange({ ...value, [kind]: e.target.value })}
            />
          </Field>
        ))}
      </div>
    </fieldset>
  );
}

function TemplateSheet({ template, onClose }: { template?: S["VmTemplate"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const [f, setF] = useState<S["VmTemplateWrite"]>(
    template
      ? {
          ...template,
          sources: template.sources.map((src) => writable(src, "in_inventory")),
        }
      : {
          key: "",
          label: "",
          sources: [{ vcenter: "", template: "", content_library: "" }],
          os_family: "linux",
          customization: "",
          description: "",
          user_data: "",
          linked_clone: false,
          cloudbase_init: false,
          offers: [{ project_id: null, environment: null }],
        },
  );
  const [tags, setTags] = useState<Record<ConsKind, string>>({
    compute: template?.constraints.compute.join(", ") ?? "",
    storage: template?.constraints.storage.join(", ") ?? "",
    network: template?.constraints.network.join(", ") ?? "",
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body: S["VmTemplateWrite"] = {
        ...f,
        user_data: f.os_family === "linux" || f.cloudbase_init ? f.user_data : "",
        cloudbase_init: f.os_family === "windows" && (f.cloudbase_init ?? false),
        constraints: { compute: tagList(tags.compute), storage: tagList(tags.storage), network: tagList(tags.network) },
      };
      return template
        ? call(api.PUT("/admin/templates/{key}", { params: { path: { key: template.key } }, body }))
        : call(api.POST("/admin/templates", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "templates"] });
      toast.success(t("infra.templates.saved", { name: f.label }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("infra.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={template ? t("infra.changeNamed", { name: template.label }) : t("infra.templates.new")}
      description={t("infra.templates.sheetLead")}
      submitLabel={save.isPending ? t("infra.saving") : t(template ? "infra.templates.save" : "infra.templates.create")}
      submitDisabled={!f.key}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[140px_1fr] gap-3">
        <Field label={t("infra.key")} htmlFor="tpl-key" error={errors["/key"]}>
          <Input
            id="tpl-key"
            className="font-mono"
            disabled={Boolean(template)}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label={t("infra.label")} htmlFor="tpl-label" error={errors["/label"]}>
          <Input id="tpl-label" value={f.label} onChange={(e) => setF({ ...f, label: e.target.value })} />
        </Field>
      </div>
      <SourceEditor
        value={f.sources}
        onChange={(sources) => setF({ ...f, sources })}
        errors={errors}
        stored={template?.sources}
      />
      <div className="flex items-start gap-2">
        <Checkbox
          id="tpl-linked"
          className="mt-0.5"
          aria-describedby="tpl-linked-note"
          checked={f.linked_clone ?? false}
          onChange={(linked_clone) => setF({ ...f, linked_clone })}
        />
        <div className="flex flex-col">
          <label htmlFor="tpl-linked" className="text-body font-medium">
            {t("infra.templates.linked")}
          </label>
          {errors["/linked_clone"] ? (
            <p id="tpl-linked-note" className="text-secondary text-bad">
              {errors["/linked_clone"]}
            </p>
          ) : (
            <p id="tpl-linked-note" className="text-secondary text-muted">
              {t("infra.templates.linkedNote")}
            </p>
          )}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("infra.templates.os")} htmlFor="tpl-os">
          <Select
            id="tpl-os"
            value={f.os_family}
            onChange={(v) => setF({ ...f, os_family: v as S["VmTemplate"]["os_family"] })}
            options={[
              { value: "linux", label: "Linux" },
              { value: "windows", label: "Windows" },
            ]}
          />
        </Field>
        <Field
          label={t("infra.templates.customization")}
          htmlFor="tpl-cust"
          hint={t("infra.templates.customizationHint")}
        >
          <Input
            id="tpl-cust"
            value={f.customization ?? ""}
            onChange={(e) => setF({ ...f, customization: e.target.value })}
          />
        </Field>
      </div>
      <Field label={t("infra.description")} htmlFor="tpl-desc">
        <Input
          id="tpl-desc"
          value={f.description ?? ""}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      {f.os_family === "windows" && (
        <div className="flex items-start gap-2">
          <Checkbox
            id="tpl-cbi"
            className="mt-0.5"
            aria-describedby="tpl-cbi-note"
            checked={f.cloudbase_init ?? false}
            onChange={(cloudbase_init) => setF({ ...f, cloudbase_init })}
          />
          <div className="flex flex-col">
            <label htmlFor="tpl-cbi" className="text-body font-medium">
              {t("infra.templates.cloudbaseInit")}
            </label>
            {errors["/cloudbase_init"] ? (
              <p id="tpl-cbi-note" className="text-secondary text-bad">
                {errors["/cloudbase_init"]}
              </p>
            ) : (
              <p id="tpl-cbi-note" className="text-secondary text-muted">
                {t("infra.templates.cloudbaseInitNote")}
              </p>
            )}
          </div>
        </div>
      )}
      {(f.os_family === "linux" || f.cloudbase_init) && (
        <Field
          label={f.os_family === "linux" ? t("infra.templates.userDataLinux") : t("infra.templates.userDataWindows")}
          htmlFor="tpl-ud"
          error={errors["/user_data"]}
          hint={
            f.os_family === "linux" ? t("infra.templates.userDataLinuxHint") : t("infra.templates.userDataWindowsHint")
          }
        >
          <Textarea
            id="tpl-ud"
            className="font-mono"
            rows={6}
            placeholder={f.os_family === "linux" ? userDataFirstLine.linux : userDataFirstLine.windows}
            value={f.user_data ?? ""}
            onChange={(e) => setF({ ...f, user_data: e.target.value })}
          />
        </Field>
      )}
      <ConstraintFields value={tags} onChange={setTags} errors={errors} />
      <OfferEditor
        value={f.offers ?? []}
        onChange={(offers) => setF({ ...f, offers })}
        error={errorAt(errors, "/offers")}
      />
    </Sheet>
  );
}

function useRemove(queryKey: string[], remove: (key: string) => Promise<unknown>) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key }: { key: string; done: string }) => remove(key),
    onSuccess: (_, { done }) => {
      void qc.invalidateQueries({ queryKey });
      toast.success(done);
    },
    onError: (e) => failed(e, t("infra.notRemoved")),
  });
}

function Templates({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const templates = useQuery(adm.templates());
  const projectName = useProjectName();
  const envName = useEnvName();
  const [editing, setEditing] = useState<S["VmTemplate"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["VmTemplate"] | null>(null);
  const remove = useRemove(["admin", "templates"], (key) =>
    call(api.DELETE("/admin/templates/{key}", { params: { path: { key } } })),
  );
  const items = templates.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("infra.templates.add")}
    </Button>
  );
  const columns: Column<S["VmTemplate"]>[] = [
    {
      id: "template",
      header: t("infra.templates.template"),
      sortValue: (tpl) => tpl.label,
      cell: (tpl) => {
        const tags = [...tpl.constraints.compute, ...tpl.constraints.storage, ...tpl.constraints.network];
        return (
          <span className="flex flex-col">
            <span>
              <span className="font-medium">{tpl.label}</span>{" "}
              <span className="font-mono text-caption text-muted">{tpl.key}</span>
              {tpl.user_data && !tpl.cloudbase_init && (
                <Badge tone="outline" className="ml-2">
                  {cloudInit}
                </Badge>
              )}
              {tpl.linked_clone && (
                <Badge tone="outline" className="ml-2">
                  {t("infra.templates.linkedBadge")}
                </Badge>
              )}
              {tpl.cloudbase_init && (
                <Badge tone="outline" className="ml-2">
                  {cloudbaseInit}
                </Badge>
              )}
            </span>
            {tags.length > 0 && <span className="font-mono text-caption text-muted">{tags.join(", ")}</span>}
          </span>
        );
      },
    },
    {
      id: "vcenter",
      header: t("infra.templates.inVcenter"),
      sortValue: (tpl) => tpl.sources.length,
      cell: (tpl) => {
        const names = [
          ...new Set(tpl.sources.map((src) => (src.content_library ? `${src.content_library} / ` : "") + src.template)),
        ];
        const missing = tpl.sources.filter((src) => src.in_inventory === false).length;
        return (
          <span className="flex flex-col">
            <span className="text-secondary">
              {t("infra.templates.vcenters", { count: tpl.sources.length })}
              {missing > 0 && (
                <Badge tone="warn" className="ml-2">
                  {missing === tpl.sources.length
                    ? t("infra.templates.notFound")
                    : t("infra.templates.someNotFound", { count: missing })}
                </Badge>
              )}
            </span>
            <span className="font-mono text-caption break-words text-muted">{names.join(", ")}</span>
          </span>
        );
      },
    },
    {
      id: "offers",
      header: t("infra.offeredTo"),
      cell: (tpl) => <span className="text-secondary text-muted">{offersText(tpl.offers, projectName, envName)}</span>,
    },
  ];
  return (
    <Panel
      title={t("infra.templates.title")}
      description={t("infra.templates.panelLead")}
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label={t("infra.templates.title")}
        columns={columns}
        rows={items}
        rowKey={(tpl) => tpl.key}
        loading={templates.isPending}
        error={templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
        empty={
          <EmptyState
            icon={HardDrive}
            title={t("infra.templates.emptyTitle")}
            body={t("infra.templates.emptyBody")}
            action={canEdit && add}
          />
        }
        rowMenuLabel={(tpl) => t("infra.actionsFor", { name: tpl.label })}
        rowMenu={
          canEdit
            ? (tpl) => (
                <>
                  <MenuItem onSelect={() => setEditing(tpl)}>{t("infra.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(tpl)}>
                    {t("infra.remove")}
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && <TemplateSheet template={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("infra.removeNamed", { name: removing.label })}
          description={
            <Trans
              t={t}
              i18nKey="infra.templates.removeLead"
              values={{ key: removing.key }}
              components={{ mono: <span className="font-mono" /> }}
            />
          }
          consequences={t("infra.templates.removeConsequences", {
            offers: offersText(removing.offers, projectName, envName),
          })}
          confirmLabel={t("infra.templates.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate(
              { key: removing.key, done: t("infra.templates.removed", { name: removing.label }) },
              { onSuccess: () => setRemoving(null) },
            )
          }
        />
      )}
    </Panel>
  );
}

function SizeSheet({ size, onClose }: { size?: S["SizePreset"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const [f, setF] = useState<S["SizePresetWrite"]>(
    size ?? {
      key: "",
      label: "",
      vcpu: 2,
      memory_gb: 4,
      disk_gb: 60,
      offers: [{ project_id: null, environment: null }],
    },
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      size
        ? call(api.PUT("/admin/sizes/{key}", { params: { path: { key: size.key } }, body: f }))
        : call(api.POST("/admin/sizes", { body: f })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sizes"] });
      toast.success(t("infra.sizes.saved", { name: f.label }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("infra.notSaved"));
    },
  });
  const num = (k: "vcpu" | "memory_gb" | "disk_gb", label: string) => (
    <Field label={label} htmlFor={`size-${k}`} error={errors[`/${k}`]}>
      <Input
        id={`size-${k}`}
        type="number"
        min={1}
        value={f[k]}
        onChange={(e) => setF({ ...f, [k]: Number(e.target.value) })}
      />
    </Field>
  );
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={size ? t("infra.changeNamed", { name: size.label }) : t("infra.sizes.new")}
      description={t("infra.sizes.sheetLead")}
      submitLabel={save.isPending ? t("infra.saving") : t(size ? "infra.sizes.save" : "infra.sizes.create")}
      submitDisabled={!f.key}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[140px_1fr] gap-3">
        <Field label={t("infra.key")} htmlFor="size-key" error={errors["/key"]}>
          <Input
            id="size-key"
            className="font-mono"
            disabled={Boolean(size)}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label={t("infra.label")} htmlFor="size-label" error={errors["/label"]}>
          <Input id="size-label" value={f.label} onChange={(e) => setF({ ...f, label: e.target.value })} />
        </Field>
      </div>
      <div className="grid grid-cols-3 gap-3">
        {num("vcpu", t("infra.sizes.vcpu"))}
        {num("memory_gb", t("infra.sizes.memory"))}
        {num("disk_gb", t("infra.sizes.disk"))}
      </div>
      <OfferEditor
        value={f.offers ?? []}
        onChange={(offers) => setF({ ...f, offers })}
        error={errorAt(errors, "/offers")}
      />
    </Sheet>
  );
}

function Sizes({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const sizes = useQuery(adm.sizes());
  const projectName = useProjectName();
  const envName = useEnvName();
  const [editing, setEditing] = useState<S["SizePreset"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["SizePreset"] | null>(null);
  const remove = useRemove(["admin", "sizes"], (key) =>
    call(api.DELETE("/admin/sizes/{key}", { params: { path: { key } } })),
  );
  const items = sizes.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("infra.sizes.add")}
    </Button>
  );
  const columns: Column<S["SizePreset"]>[] = [
    {
      id: "size",
      header: t("infra.sizes.size"),
      cell: (z) => (
        <>
          <span className="font-medium">{z.label}</span>{" "}
          <span className="font-mono text-caption text-muted">{z.key}</span>
        </>
      ),
    },
    {
      id: "shape",
      header: t("infra.sizes.shape"),
      sortValue: (z) => z.vcpu * 1000 + z.memory_gb,
      cell: (z) => (
        <span className="tnum">
          {t("infra.sizes.shapeValue", {
            vcpu: z.vcpu,
            memory: formatNumber(z.memory_gb),
            disk: formatNumber(z.disk_gb),
          })}
        </span>
      ),
    },
    {
      id: "offers",
      header: t("infra.offeredTo"),
      cell: (z) => <span className="text-secondary text-muted">{offersText(z.offers, projectName, envName)}</span>,
    },
  ];
  return (
    <Panel
      title={t("infra.sizes.title")}
      description={t("infra.sizes.panelLead")}
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label={t("infra.sizes.title")}
        columns={columns}
        rows={items}
        rowKey={(z) => z.key}
        loading={sizes.isPending}
        error={sizes.isError && <ErrorState error={sizes.error} onRetry={() => void sizes.refetch()} />}
        empty={
          <EmptyState
            icon={Cpu}
            title={t("infra.sizes.emptyTitle")}
            body={t("infra.sizes.emptyBody")}
            action={canEdit && add}
          />
        }
        rowMenuLabel={(z) => t("infra.actionsFor", { name: z.label })}
        rowMenu={
          canEdit
            ? (z) => (
                <>
                  <MenuItem onSelect={() => setEditing(z)}>{t("infra.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(z)}>
                    {t("infra.remove")}
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && <SizeSheet size={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("infra.removeNamed", { name: removing.label })}
          description={
            <Trans
              t={t}
              i18nKey="infra.sizes.removeLead"
              values={{ key: removing.key }}
              components={{ mono: <span className="font-mono" /> }}
            />
          }
          consequences={t("infra.sizes.removeConsequences", {
            offers: offersText(removing.offers, projectName, envName),
          })}
          confirmLabel={t("infra.sizes.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate(
              { key: removing.key, done: t("infra.sizes.removed", { name: removing.label }) },
              { onSuccess: () => setRemoving(null) },
            )
          }
        />
      )}
    </Panel>
  );
}

function MappingSheet({ mapping, onClose }: { mapping?: S["NetworkMapping"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const [network, setNetwork] = useState(mapping?.network ?? "");
  const [f, setF] = useState({
    subnet: mapping?.subnet ?? "",
    gateway: mapping?.gateway ?? "",
    dns_zone: mapping?.dns_zone ?? "",
    dns_servers: (mapping?.dns_servers ?? []).join(", "),
    subnet6: mapping?.subnet6 ?? "",
    gateway6: mapping?.gateway6 ?? "",
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/network-mappings/{network}", {
          params: { path: { network } },
          body: {
            ...f,
            dns_servers: f.dns_servers
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "network-mappings"] });
      void qc.invalidateQueries({ queryKey: ["admin", "placement-preview"] });
      toast.success(t("infra.networks.mapped", { network }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("infra.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={mapping ? t("infra.networks.byHandTitle", { network: mapping.network }) : t("infra.networks.new")}
      description={t("infra.networks.sheetLead")}
      submitLabel={save.isPending ? t("infra.saving") : t("infra.networks.save")}
      submitDisabled={!network}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label={t("infra.networks.network")} htmlFor="map-network">
        <Input
          id="map-network"
          className="font-mono"
          disabled={Boolean(mapping)}
          value={network}
          onChange={(e) => setNetwork(e.target.value)}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("infra.networks.subnet")} htmlFor="map-subnet" error={errors["/subnet"]}>
          <Input
            id="map-subnet"
            className="font-mono"
            value={f.subnet}
            onChange={(e) => setF({ ...f, subnet: e.target.value })}
          />
        </Field>
        <Field label={t("infra.networks.gateway")} htmlFor="map-gateway" error={errors["/gateway"]}>
          <Input
            id="map-gateway"
            className="font-mono"
            value={f.gateway}
            onChange={(e) => setF({ ...f, gateway: e.target.value })}
          />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field
          label={t("infra.networks.subnet6")}
          htmlFor="map-subnet6"
          hint={t("infra.networks.subnet6Hint")}
          error={errors["/subnet6"]}
        >
          <Input
            id="map-subnet6"
            className="font-mono"
            value={f.subnet6}
            onChange={(e) => setF({ ...f, subnet6: e.target.value })}
          />
        </Field>
        <Field label={t("infra.networks.gateway6")} htmlFor="map-gateway6" error={errors["/gateway6"]}>
          <Input
            id="map-gateway6"
            className="font-mono"
            value={f.gateway6}
            onChange={(e) => setF({ ...f, gateway6: e.target.value })}
          />
        </Field>
      </div>
      <Field label={t("infra.networks.zone")} htmlFor="map-zone" error={errors["/dns_zone"]}>
        <Input id="map-zone" value={f.dns_zone} onChange={(e) => setF({ ...f, dns_zone: e.target.value })} />
      </Field>
      <Field label={t("infra.networks.servers")} htmlFor="map-dns" hint={t("infra.networks.commaSeparated")}>
        <Input
          id="map-dns"
          className="font-mono"
          value={f.dns_servers}
          onChange={(e) => setF({ ...f, dns_servers: e.target.value })}
        />
      </Field>
    </Sheet>
  );
}

function Networks({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const mappings = useQuery(adm.networkMappings());
  const [editing, setEditing] = useState<S["NetworkMapping"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["NetworkMapping"] | null>(null);
  const remove = useRemove(["admin", "network-mappings"], (network) =>
    call(api.DELETE("/admin/network-mappings/{network}", { params: { path: { network } } })),
  );
  const items = mappings.data?.items;
  // Where a network has IP ranges, its VMs take their addresses from them, not from its other subnet (ADR-0063).
  const rangedIn = (network: string) =>
    (items ?? []).filter((m) => m.source === "rvav" && m.network === network && m.vcenter).map((m) => m.vcenter!);
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("infra.networks.add")}
    </Button>
  );
  const columns: Column<S["NetworkMapping"]>[] = [
    {
      id: "network",
      header: t("infra.networks.networkColumn"),
      sortValue: (m) => m.network,
      cell: (m) => (
        <span className="font-mono text-secondary">
          {m.network}
          {m.vcenter && <span className="block text-caption text-muted">{m.vcenter}</span>}
        </span>
      ),
    },
    {
      id: "subnet",
      header: t("infra.networks.subnet"),
      cell: (m) => (
        <span className="font-mono text-secondary">
          {m.subnet} <span className="text-muted">{t("infra.networks.via", { gateway: m.gateway })}</span>
          {m.subnet6 && (
            <span className="block">
              {m.subnet6} <span className="text-muted">{t("infra.networks.via", { gateway: m.gateway6 })}</span>
            </span>
          )}
        </span>
      ),
    },
    {
      id: "dns",
      header: t("infra.networks.dns"),
      cell: (m) => (
        <span className="text-secondary">
          {m.source === "rvav" && !m.dns_zone ? t("infra.networks.blueprintDomain") : m.dns_zone}{" "}
          <span className="font-mono text-muted">{m.dns_servers.join(", ")}</span>
          {m.source === "rvav" && (
            <span className="block text-caption text-muted">{t("infra.networks.noRecords")}</span>
          )}
        </span>
      ),
    },
    {
      id: "source",
      header: t("infra.networks.fromColumn"),
      sortValue: (m) => t(`infra.networks.from.${m.source}`),
      cell: (m) =>
        m.source === "rvav" ? (
          <Link
            to="/admin/infrastructure"
            search={{ tab: "ip-ranges" }}
            className="text-secondary font-medium text-accent hover:underline"
          >
            {t("infra.networks.from.rvav")}
          </Link>
        ) : (
          <>
            <Badge tone={m.source === "manual" ? "accent" : "neutral"}>{t(`infra.networks.from.${m.source}`)}</Badge>
            {rangedIn(m.network).length > 0 && (
              <span className="mt-1 block text-caption text-muted">
                {t("infra.networks.rangesWin", { vcenters: rangedIn(m.network).join(", ") })}
              </span>
            )}
          </>
        ),
    },
  ];
  return (
    <Panel
      title={t("infra.networks.title")}
      description={t("infra.networks.panelLead")}
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label={t("infra.networks.title")}
        columns={columns}
        rows={items}
        rowKey={(m) => `${m.network}/${m.vcenter ?? ""}`}
        minWidth={720}
        loading={mappings.isPending}
        error={mappings.isError && <ErrorState error={mappings.error} onRetry={() => void mappings.refetch()} />}
        empty={
          <EmptyState
            icon={Network}
            title={t("infra.networks.emptyTitle")}
            body={t("infra.networks.emptyBody")}
            action={canEdit && add}
          />
        }
        rowMenuLabel={(m) => t("infra.actionsFor", { name: m.network })}
        rowMenu={
          canEdit
            ? (m) =>
                // A segment made for a deployment goes with it (ADR-0028).
                m.source === "nsx" || m.source === "rvav" ? null : (
                  <>
                    <MenuItem onSelect={() => setEditing(m)}>
                      {m.source === "manual" ? t("infra.change") : t("infra.networks.byHand")}
                    </MenuItem>
                    {m.source === "manual" && (
                      <>
                        <MenuSeparator />
                        <MenuItem danger onSelect={() => setRemoving(m)}>
                          {t("infra.remove")}
                        </MenuItem>
                      </>
                    )}
                  </>
                )
            : undefined
        }
      />
      {editing && <MappingSheet mapping={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("infra.networks.removeTitle", { network: removing.network })}
          description={t("infra.networks.removeLead", { subnet: removing.subnet })}
          consequences={t("infra.networks.removeConsequences")}
          confirmLabel={t("infra.networks.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate(
              { key: removing.network, done: t("infra.networks.removed", { network: removing.network }) },
              { onSuccess: () => setRemoving(null) },
            )
          }
        />
      )}
    </Panel>
  );
}

export type Kind = "cluster" | "host" | "resource_pool" | "datastore" | "datastore_cluster" | "network";

interface VsphereObject {
  kind: Kind;
  vcenter: string;
  name: string;
  /** A resource pool's cluster: pools of two clusters share paths. */
  cluster?: string;
  /** Hosts and free memory, free space, or the clusters a network reaches. */
  detail: string;
  tags: string[];
}

export const inventoryKinds = [
  "cluster",
  "host",
  "resource_pool",
  "datastore",
  "datastore_cluster",
  "network",
] as const;

/** The inventory's filters, in the address beside the tab. */
export interface InventorySearch {
  q?: string;
  kind?: Kind;
}

type T = TFunction<"admin-governance">;

function poolLimits(p: S["InventoryResourcePool"], t: T): string {
  const limits = [
    p.memory_limit_gb != null ? t("infra.inventory.poolMemory", { gb: formatNumber(p.memory_limit_gb) }) : undefined,
    p.cpu_limit_mhz != null ? t("infra.inventory.poolCpu", { mhz: formatNumber(p.cpu_limit_mhz) }) : undefined,
  ].filter((l) => l !== undefined);
  return limits.length ? t("infra.inventory.limitedTo", { limits: formatList(limits) }) : t("infra.inventory.noLimits");
}

function objectsOf(inv: S["VsphereInventory"], t: T): VsphereObject[] {
  return [
    ...inv.clusters.map((c) => ({
      kind: "cluster" as const,
      vcenter: c.vcenter,
      name: c.name,
      detail:
        t("infra.inventory.cluster", { count: c.hosts, free: formatNumber(c.memory_free_gb) }) +
        (c.memory_reserved_gb > 0
          ? t("infra.inventory.clusterHeld", { reserved: formatNumber(c.memory_reserved_gb) })
          : ""),
      tags: c.tags,
    })),
    ...inv.hosts.map((h) => ({
      kind: "host" as const,
      vcenter: h.vcenter,
      name: h.name,
      detail: t(`infra.inventory.host.${h.state}`, {
        cluster: h.cluster,
        free: formatNumber(h.memory_free_gb),
        total: formatNumber(h.memory_total_gb),
      }),
      tags: h.tags,
    })),
    ...inv.resource_pools.map((p) => ({
      kind: "resource_pool" as const,
      vcenter: p.vcenter,
      name: p.path,
      cluster: p.cluster,
      detail: t("infra.inventory.pool", { cluster: p.cluster, limits: poolLimits(p, t) }),
      tags: p.tags,
    })),
    ...inv.datastore_clusters.map((d) => ({
      kind: "datastore_cluster" as const,
      vcenter: d.vcenter,
      name: d.name,
      detail: t(d.sdrs ? "infra.inventory.datastoreCluster.on" : "infra.inventory.datastoreCluster.off", {
        free: formatNumber(d.free_gb),
        capacity: formatNumber(d.capacity_gb),
        datastores: d.datastores.join(", ") || t("infra.inventory.noDatastore"),
      }),
      tags: d.tags,
    })),
    ...inv.datastores.map((d) => ({
      kind: "datastore" as const,
      vcenter: d.vcenter,
      name: d.name,
      detail: t("infra.inventory.datastore", { free: formatNumber(d.free_gb), capacity: formatNumber(d.capacity_gb) }),
      tags: d.tags,
    })),
    ...inv.networks.map((n) => ({
      kind: "network" as const,
      vcenter: n.vcenter,
      name: n.name,
      detail: n.clusters.join(", ") || t("infra.inventory.noCluster"),
      tags: n.tags,
    })),
  ];
}

/** The vCenter objects the sync read, with the tags placement rules and constraints choose by (ADR-0035). */
function Inventory() {
  const { t } = useTranslation("admin-governance");
  const inventory = useQuery(adm.inventory());
  const search: InventorySearch = useSearch({ from: "/admin/infrastructure" });
  const filters = useAddressFilters(search, ["q", "kind"]);
  const all = inventory.data ? objectsOf(inventory.data, t) : undefined;
  const needle = (search.q ?? "").toLowerCase();
  const rows = all?.filter(
    (o) =>
      (!search.kind || o.kind === search.kind) &&
      (!needle || o.name.toLowerCase().includes(needle) || o.tags.some((tag) => tag.toLowerCase().includes(needle))),
  );
  const columns: Column<VsphereObject>[] = [
    {
      id: "name",
      header: t("infra.inventory.name"),
      sortValue: (o) => o.name,
      cell: (o) => <span className="font-mono text-secondary">{o.name}</span>,
    },
    {
      id: "kind",
      header: t("infra.inventory.kindColumn"),
      sortValue: (o) => o.kind,
      cell: (o) => t(`infra.inventory.kind.${o.kind}`),
    },
    { id: "vcenter", header: t("infra.inventory.vcenter"), sortValue: (o) => o.vcenter, cell: (o) => o.vcenter },
    {
      id: "detail",
      header: t("infra.inventory.detail"),
      cell: (o) => <span className="text-secondary">{o.detail}</span>,
    },
    {
      id: "tags",
      header: t("infra.inventory.tags"),
      sortValue: (o) => o.tags.join(" ") || null,
      cell: (o) =>
        o.tags.length ? (
          <span className="flex flex-wrap gap-1">
            {o.tags.map((tag) => (
              <Badge key={tag} tone="outline" className="font-mono">
                {tag}
              </Badge>
            ))}
          </span>
        ) : (
          <span className="text-faint">{t("infra.inventory.none")}</span>
        ),
    },
  ];
  return (
    <Panel title={t("infra.inventory.title")} description={t("infra.inventory.lead")} flush>
      <DataTable
        label={t("infra.inventory.title")}
        columns={columns}
        rows={rows}
        rowKey={(o) => `${o.kind}/${o.vcenter}/${o.cluster ?? ""}/${o.name}`}
        minWidth={760}
        defaultSort={{ id: "name", desc: false }}
        loading={inventory.isPending}
        error={inventory.isError && <ErrorState error={inventory.error} onRetry={() => void inventory.refetch()} />}
        toolbar={
          <FilterBar
            filters={filters}
            search={{ label: t("infra.inventory.search"), placeholder: t("infra.inventory.searchPlaceholder") }}
          >
            <FilterSelect
              name="kind"
              label={t("infra.inventory.kindColumn")}
              any={t("infra.inventory.kinds.all")}
              options={inventoryKinds.map((value) => ({ value, label: t(`infra.inventory.kinds.${value}`) }))}
              className="w-40"
            />
          </FilterBar>
        }
        empty={
          filters.filtered ? (
            <FilterEmpty
              filters={filters}
              icon={Server}
              title={t("infra.inventory.nothingMatches")}
              body={t("infra.inventory.nothingMatchesBody")}
            />
          ) : (
            <EmptyState icon={Server} title={t("infra.inventory.emptyTitle")} body={t("infra.inventory.emptyBody")} />
          )
        }
      />
    </Panel>
  );
}

export const infrastructureTabs = ["templates", "sizes", "networks", "ip-ranges", "inventory"] as const;
export type InfrastructureTab = (typeof infrastructureTabs)[number];

export function InfrastructurePage({ tab = "templates" }: { tab?: InfrastructureTab }) {
  const { t } = useTranslation(["admin-governance", "shell"]);
  const { me } = useCurrentProject();
  const navigate = useNavigate();
  const canEdit = can(me, "infrastructure.manage");
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.infrastructure")} description={t("infra.lead")} />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({ to: "/admin/infrastructure", search: { tab: next as InfrastructureTab }, replace: true })
        }
      >
        <TabsList aria-label={t("infra.tabsLabel")}>
          <TabsTrigger value="templates">{t("infra.tabs.templates")}</TabsTrigger>
          <TabsTrigger value="sizes">{t("infra.tabs.sizes")}</TabsTrigger>
          <TabsTrigger value="networks">{t("infra.tabs.networks")}</TabsTrigger>
          <TabsTrigger value="ip-ranges">{t("infra.tabs.ipRanges")}</TabsTrigger>
          <TabsTrigger value="inventory">{t("infra.tabs.inventory")}</TabsTrigger>
        </TabsList>
        <TabsContent value="templates" className="pt-4">
          <Templates canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="sizes" className="pt-4">
          <Sizes canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="networks" className="pt-4">
          <Networks canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="ip-ranges" className="pt-4">
          <IPRanges canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="inventory" className="pt-4">
          <Inventory />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
