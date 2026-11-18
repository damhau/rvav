// VM templates, size presets and network mappings (S9, ADR-0020): what
// blueprints' template and size pickers offer, and the subnet each vCenter
// network deploys into; and the vCenter objects' tags (ADR-0035).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Cpu, HardDrive, Network, Server, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, TableSearch, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, OfferEditor, offersText, writable } from "./common";

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
  const vcenters = useQuery(adm.vcenters());
  const primary = vcenters.data?.items.find((v) => v.primary)?.name ?? "";
  const options = (vcenters.data?.items ?? []).map((v) => ({
    value: v.name || unnamed,
    label: v.name || "The vCenter integration's",
    meta: v.primary ? "Integration" : undefined,
  }));
  const valueOf = (src: Source) => src.vcenter || primary || unnamed;
  for (const src of value) {
    if (!options.some((o) => o.value === valueOf(src)))
      options.push({ value: valueOf(src), label: src.vcenter || "The vCenter integration's", meta: undefined });
  }
  const set = (i: number, src: Source) => onChange(value.map((x, j) => (j === i ? src : x)));
  const unused = options.find((o) => !value.some((src) => valueOf(src) === o.value));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">In vCenter</legend>
      <p className="text-secondary text-muted">
        Its template in each vCenter. VMs go to the clusters of these vCenters only, cloned from that vCenter's.
      </p>
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
            aria-label={`Source ${i + 1}`}
          >
            <div className="flex items-end gap-2">
              <Field label="vCenter" htmlFor={`tpl-src-${i}-vc`} error={errors[`${at}/vcenter`]} className="flex-1">
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
                  Not found
                </Badge>
              )}
              <Button
                type="button"
                size="icon"
                variant="quiet"
                aria-label={`Remove source ${i + 1}`}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" />
              </Button>
            </div>
            <div className="grid grid-cols-[1fr_160px] gap-2">
              <Field label="Template" htmlFor={`tpl-src-${i}-tpl`} error={errors[`${at}/template`]}>
                <Input
                  id={`tpl-src-${i}-tpl`}
                  className="font-mono"
                  value={src.template}
                  onChange={(e) => set(i, { ...src, template: e.target.value })}
                />
              </Field>
              <Field label="Content library" htmlFor={`tpl-src-${i}-lib`} hint="When it is an item of one">
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
            Add a vCenter
          </Button>
        </div>
      )}
    </fieldset>
  );
}

type ConsKind = keyof S["TemplateConstraints"];
const kinds: { kind: ConsKind; label: string }[] = [
  { kind: "compute", label: "Compute" },
  { kind: "storage", label: "Storage" },
  { kind: "network", label: "Network" },
];
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
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">Constraint tags</legend>
      <p className="text-secondary text-muted">
        Added to the item's, separated by commas: <span className="font-mono">rvav-os/rhel9</span>,{" "}
        <span className="font-mono">!rvav-zone/pci</span>, <span className="font-mono">rvav-tier/gold:soft</span>.
      </p>
      <div className="grid grid-cols-3 gap-2">
        {kinds.map(({ kind, label }) => (
          <Field key={kind} label={label} htmlFor={`tpl-cons-${kind}`} error={errorAt(errors, `/constraints/${kind}`)}>
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
        user_data: f.os_family === "linux" ? f.user_data : "",
        constraints: { compute: tagList(tags.compute), storage: tagList(tags.storage), network: tagList(tags.network) },
      };
      return template
        ? call(api.PUT("/admin/templates/{key}", { params: { path: { key: template.key } }, body }))
        : call(api.POST("/admin/templates", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "templates"] });
      toast.success(`${f.label} saved`);
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
      title={template ? `Change ${template.label}` : "New VM template"}
      description="VM forms offer it in their template picker, to the projects and environments below."
      submitLabel="Save template"
      submitDisabled={!f.key || save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[140px_1fr] gap-3">
        <Field label="Key" htmlFor="tpl-key" error={errors["/key"]}>
          <Input
            id="tpl-key"
            className="font-mono"
            disabled={Boolean(template)}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label="Label" htmlFor="tpl-label" error={errors["/label"]}>
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
            Linked clones
          </label>
          {errors["/linked_clone"] ? (
            <p id="tpl-linked-note" className="text-secondary text-bad">
              {errors["/linked_clone"]}
            </p>
          ) : (
            <p id="tpl-linked-note" className="text-secondary text-muted">
              Clone its VMs from the vCenter template&apos;s current snapshot, unless the blueprint says otherwise. Not
              for content library items; the VMs keep the template&apos;s disk size.
            </p>
          )}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Operating system" htmlFor="tpl-os">
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
        <Field label="Customization spec" htmlFor="tpl-cust" hint="Empty for rvav's own">
          <Input
            id="tpl-cust"
            value={f.customization ?? ""}
            onChange={(e) => setF({ ...f, customization: e.target.value })}
          />
        </Field>
      </div>
      <Field label="Description" htmlFor="tpl-desc">
        <Input
          id="tpl-desc"
          value={f.description ?? ""}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      {f.os_family === "linux" && (
        <Field
          label="Cloud-init user data"
          htmlFor="tpl-ud"
          error={errors["/user_data"]}
          hint="Merged with the item's. A Go template, as a blueprint's: print inputs through json. Empty for none."
        >
          <Textarea
            id="tpl-ud"
            className="font-mono"
            rows={6}
            placeholder="#cloud-config"
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
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key }: { key: string; label: string }) => remove(key),
    onSuccess: (_, { label }) => {
      void qc.invalidateQueries({ queryKey });
      toast.success(`${label} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
}

function Templates({ canEdit }: { canEdit: boolean }) {
  const templates = useQuery(adm.templates());
  const projectName = useProjectName();
  const [editing, setEditing] = useState<S["VmTemplate"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["VmTemplate"] | null>(null);
  const remove = useRemove(["admin", "templates"], (key) =>
    call(api.DELETE("/admin/templates/{key}", { params: { path: { key } } })),
  );
  const items = templates.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New template
    </Button>
  );
  const columns: Column<S["VmTemplate"]>[] = [
    {
      id: "template",
      header: "Template",
      sortValue: (t) => t.label,
      cell: (t) => {
        const tags = [...t.constraints.compute, ...t.constraints.storage, ...t.constraints.network];
        return (
          <span className="flex flex-col">
            <span>
              <span className="font-medium">{t.label}</span>{" "}
              <span className="font-mono text-caption text-muted">{t.key}</span>
              {t.user_data && (
                <Badge tone="outline" className="ml-2">
                  cloud-init
                </Badge>
              )}
              {t.linked_clone && (
                <Badge tone="outline" className="ml-2">
                  linked clones
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
      header: "In vCenter",
      sortValue: (t) => t.sources.length,
      cell: (t) => {
        const names = [
          ...new Set(t.sources.map((src) => (src.content_library ? `${src.content_library} / ` : "") + src.template)),
        ];
        const missing = t.sources.filter((src) => src.in_inventory === false).length;
        return (
          <span className="flex flex-col">
            <span className="text-secondary">
              {t.sources.length} vCenter{t.sources.length === 1 ? "" : "s"}
              {missing > 0 && (
                <Badge tone="warn" className="ml-2">
                  {missing === t.sources.length ? "Not found" : `${missing} not found`}
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
      header: "Offered to",
      cell: (t) => <span className="text-secondary text-muted">{offersText(t.offers, projectName)}</span>,
    },
  ];
  return (
    <Panel
      title="VM templates"
      description="What VM forms offer in their template picker, and to which projects."
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label="VM templates"
        columns={columns}
        rows={items}
        rowKey={(t) => t.key}
        loading={templates.isPending}
        error={templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
        empty={
          <EmptyState
            icon={HardDrive}
            title="No templates yet"
            body="Blueprints name a vCenter template themselves until you add templates for their pickers."
            action={canEdit && add}
          />
        }
        rowMenuLabel={(t) => `Actions for ${t.label}`}
        rowMenu={
          canEdit
            ? (t) => (
                <>
                  <MenuItem onSelect={() => setEditing(t)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(t)}>
                    Remove
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
          title={`Remove ${removing.label}?`}
          description={
            <>
              VM forms stop offering <span className="font-mono">{removing.key}</span> in their template picker.
            </>
          }
          consequences={`Offered to ${offersText(removing.offers, projectName)}. A blueprint that names its own template keeps it, and VMs built from this one aren't changed.`}
          confirmLabel="Remove template"
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate({ key: removing.key, label: removing.label }, { onSuccess: () => setRemoving(null) })
          }
        />
      )}
    </Panel>
  );
}

function SizeSheet({ size, onClose }: { size?: S["SizePreset"]; onClose: () => void }) {
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
      toast.success(`${f.label} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
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
      title={size ? `Change ${size.label}` : "New size"}
      description="VM forms offer it in their size picker, to the projects and environments below."
      submitLabel="Save size"
      submitDisabled={!f.key || save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[140px_1fr] gap-3">
        <Field label="Key" htmlFor="size-key" error={errors["/key"]}>
          <Input
            id="size-key"
            className="font-mono"
            disabled={Boolean(size)}
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
        </Field>
        <Field label="Label" htmlFor="size-label" error={errors["/label"]}>
          <Input id="size-label" value={f.label} onChange={(e) => setF({ ...f, label: e.target.value })} />
        </Field>
      </div>
      <div className="grid grid-cols-3 gap-3">
        {num("vcpu", "vCPU")}
        {num("memory_gb", "Memory (GB)")}
        {num("disk_gb", "System disk (GB)")}
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
  const sizes = useQuery(adm.sizes());
  const projectName = useProjectName();
  const [editing, setEditing] = useState<S["SizePreset"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["SizePreset"] | null>(null);
  const remove = useRemove(["admin", "sizes"], (key) =>
    call(api.DELETE("/admin/sizes/{key}", { params: { path: { key } } })),
  );
  const items = sizes.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New size
    </Button>
  );
  const columns: Column<S["SizePreset"]>[] = [
    {
      id: "size",
      header: "Size",
      cell: (z) => (
        <>
          <span className="font-medium">{z.label}</span>{" "}
          <span className="font-mono text-caption text-muted">{z.key}</span>
        </>
      ),
    },
    {
      id: "shape",
      header: "vCPU, memory, disk",
      sortValue: (z) => z.vcpu * 1000 + z.memory_gb,
      cell: (z) => (
        <span className="tnum">
          {z.vcpu} vCPU, {z.memory_gb} GB, {z.disk_gb} GB
        </span>
      ),
    },
    {
      id: "offers",
      header: "Offered to",
      cell: (z) => <span className="text-secondary text-muted">{offersText(z.offers, projectName)}</span>,
    },
  ];
  return (
    <Panel
      title="Sizes"
      description="What VM forms offer in their size picker: vCPU, memory and system disk."
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label="Sizes"
        columns={columns}
        rows={items}
        rowKey={(z) => z.key}
        loading={sizes.isPending}
        error={sizes.isError && <ErrorState error={sizes.error} onRetry={() => void sizes.refetch()} />}
        empty={
          <EmptyState
            icon={Cpu}
            title="No sizes yet"
            body="Blueprints set their own vCPU and memory until you add sizes for their pickers."
            action={canEdit && add}
          />
        }
        rowMenuLabel={(z) => `Actions for ${z.label}`}
        rowMenu={
          canEdit
            ? (z) => (
                <>
                  <MenuItem onSelect={() => setEditing(z)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(z)}>
                    Remove
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
          title={`Remove ${removing.label}?`}
          description={
            <>
              VM forms stop offering <span className="font-mono">{removing.key}</span> in their size picker.
            </>
          }
          consequences={`Offered to ${offersText(removing.offers, projectName)}. A blueprint that sets its own size keeps it, and VMs of this size aren't changed.`}
          confirmLabel="Remove size"
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate({ key: removing.key, label: removing.label }, { onSuccess: () => setRemoving(null) })
          }
        />
      )}
    </Panel>
  );
}

function MappingSheet({ mapping, onClose }: { mapping?: S["NetworkMapping"]; onClose: () => void }) {
  const qc = useQueryClient();
  const [network, setNetwork] = useState(mapping?.network ?? "");
  const [f, setF] = useState({
    subnet: mapping?.subnet ?? "",
    gateway: mapping?.gateway ?? "",
    dns_zone: mapping?.dns_zone ?? "",
    dns_servers: (mapping?.dns_servers ?? []).join(", "),
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
      toast.success(`${network} mapped`);
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
      title={mapping ? `Map ${mapping.network} by hand` : "Map a network"}
      description="A mapping by hand wins over EfficientIP's until it is removed."
      submitLabel="Save mapping"
      submitDisabled={!network || save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label="vCenter network" htmlFor="map-network">
        <Input
          id="map-network"
          className="font-mono"
          disabled={Boolean(mapping)}
          value={network}
          onChange={(e) => setNetwork(e.target.value)}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Subnet" htmlFor="map-subnet" error={errors["/subnet"]}>
          <Input
            id="map-subnet"
            className="font-mono"
            value={f.subnet}
            onChange={(e) => setF({ ...f, subnet: e.target.value })}
          />
        </Field>
        <Field label="Gateway" htmlFor="map-gateway" error={errors["/gateway"]}>
          <Input
            id="map-gateway"
            className="font-mono"
            value={f.gateway}
            onChange={(e) => setF({ ...f, gateway: e.target.value })}
          />
        </Field>
      </div>
      <Field label="DNS zone" htmlFor="map-zone" error={errors["/dns_zone"]}>
        <Input id="map-zone" value={f.dns_zone} onChange={(e) => setF({ ...f, dns_zone: e.target.value })} />
      </Field>
      <Field label="DNS servers" htmlFor="map-dns" hint="Comma-separated">
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

const sourceLabel = { manual: "By hand", nsx: "NSX segment", efficientip: "EfficientIP" } as const;

function Networks({ canEdit }: { canEdit: boolean }) {
  const mappings = useQuery(adm.networkMappings());
  const [editing, setEditing] = useState<S["NetworkMapping"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["NetworkMapping"] | null>(null);
  const remove = useRemove(["admin", "network-mappings"], (network) =>
    call(api.DELETE("/admin/network-mappings/{network}", { params: { path: { network } } })),
  );
  const items = mappings.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      Map a network
    </Button>
  );
  const columns: Column<S["NetworkMapping"]>[] = [
    {
      id: "network",
      header: "Network",
      sortValue: (m) => m.network,
      cell: (m) => <span className="font-mono text-secondary">{m.network}</span>,
    },
    {
      id: "subnet",
      header: "Subnet",
      cell: (m) => (
        <span className="font-mono text-secondary">
          {m.subnet} <span className="text-muted">via {m.gateway}</span>
        </span>
      ),
    },
    {
      id: "dns",
      header: "DNS",
      cell: (m) => (
        <span className="text-secondary">
          {m.dns_zone} <span className="font-mono text-muted">{m.dns_servers.join(", ")}</span>
        </span>
      ),
    },
    {
      id: "source",
      header: "From",
      sortValue: (m) => sourceLabel[m.source],
      cell: (m) => <Badge tone={m.source === "manual" ? "accent" : "neutral"}>{sourceLabel[m.source]}</Badge>,
    },
  ];
  return (
    <Panel
      title="Network mappings"
      description="EfficientIP's subnets come with their vCenter network in the sync; map a network by hand when EfficientIP doesn't say."
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label="Network mappings"
        columns={columns}
        rows={items}
        rowKey={(m) => m.network}
        minWidth={720}
        loading={mappings.isPending}
        error={mappings.isError && <ErrorState error={mappings.error} onRetry={() => void mappings.refetch()} />}
        empty={
          <EmptyState
            icon={Network}
            title="No networks mapped"
            body="The EfficientIP sync maps the networks it knows. Map one by hand when it doesn't."
            action={canEdit && add}
          />
        }
        rowMenuLabel={(m) => `Actions for ${m.network}`}
        rowMenu={
          canEdit
            ? (m) =>
                // A segment made for a deployment goes with it (ADR-0028).
                m.source === "nsx" ? null : (
                  <>
                    <MenuItem onSelect={() => setEditing(m)}>
                      {m.source === "manual" ? "Change" : "Map by hand"}
                    </MenuItem>
                    {m.source === "manual" && (
                      <>
                        <MenuSeparator />
                        <MenuItem danger onSelect={() => setRemoving(m)}>
                          Remove
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
          title={`Remove the mapping of ${removing.network}?`}
          description={`Its subnet ${removing.subnet}, set by hand, stops applying.`}
          consequences="The next EfficientIP sync brings back EfficientIP's subnet for the network, if it has one. VMs already on the network keep their addresses."
          confirmLabel="Remove mapping"
          pending={remove.isPending}
          onConfirm={() =>
            remove.mutate(
              { key: removing.network, label: `The mapping of ${removing.network}` },
              { onSuccess: () => setRemoving(null) },
            )
          }
        />
      )}
    </Panel>
  );
}

type Kind = "cluster" | "host" | "resource_pool" | "datastore" | "datastore_cluster" | "network";

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

const kindLabel: Record<Kind, string> = {
  cluster: "Cluster",
  host: "Host",
  resource_pool: "Resource pool",
  datastore: "Datastore",
  datastore_cluster: "Datastore cluster",
  network: "Network",
};

const hostState: Record<S["InventoryHost"]["state"], string> = {
  connected: "",
  maintenance: ", in maintenance",
  disconnected: ", disconnected",
};

function poolLimits(p: S["InventoryResourcePool"]): string {
  const limits = [
    p.memory_limit_gb != null && `${p.memory_limit_gb} GB memory`,
    p.cpu_limit_mhz != null && `${p.cpu_limit_mhz} MHz CPU`,
  ].filter(Boolean);
  return limits.length ? `limited to ${limits.join(" and ")}` : "no limits";
}

function objectsOf(inv: S["VsphereInventory"]): VsphereObject[] {
  return [
    ...inv.clusters.map((c) => ({
      kind: "cluster" as const,
      vcenter: c.vcenter,
      name: c.name,
      detail: `${c.hosts} hosts, ${c.memory_free_gb} GB memory free`,
      tags: c.tags,
    })),
    ...inv.hosts.map((h) => ({
      kind: "host" as const,
      vcenter: h.vcenter,
      name: h.name,
      detail: `In ${h.cluster}, ${h.memory_free_gb} of ${h.memory_total_gb} GB memory free${hostState[h.state]}`,
      tags: h.tags,
    })),
    ...inv.resource_pools.map((p) => ({
      kind: "resource_pool" as const,
      vcenter: p.vcenter,
      name: p.path,
      cluster: p.cluster,
      detail: `In ${p.cluster}, ${poolLimits(p)}`,
      tags: p.tags,
    })),
    ...inv.datastore_clusters.map((d) => ({
      kind: "datastore_cluster" as const,
      vcenter: d.vcenter,
      name: d.name,
      detail: `${d.free_gb} of ${d.capacity_gb} GB free on ${d.datastores.join(", ") || "no datastore"}, Storage DRS ${d.sdrs ? "on" : "off"}`,
      tags: d.tags,
    })),
    ...inv.datastores.map((d) => ({
      kind: "datastore" as const,
      vcenter: d.vcenter,
      name: d.name,
      detail: `${d.free_gb} of ${d.capacity_gb} GB free`,
      tags: d.tags,
    })),
    ...inv.networks.map((n) => ({
      kind: "network" as const,
      vcenter: n.vcenter,
      name: n.name,
      detail: n.clusters.join(", ") || "No cluster",
      tags: n.tags,
    })),
  ];
}

/** The vCenter objects the sync read, with the tags placement rules and constraints choose by (ADR-0035). */
function Inventory() {
  const inventory = useQuery(adm.inventory());
  const [q, setQ] = useState("");
  const [kind, setKind] = useState<Kind | "all">("all");
  const all = inventory.data ? objectsOf(inventory.data) : undefined;
  const needle = q.trim().toLowerCase();
  const rows = all?.filter(
    (o) =>
      (kind === "all" || o.kind === kind) &&
      (!needle || o.name.toLowerCase().includes(needle) || o.tags.some((t) => t.toLowerCase().includes(needle))),
  );
  const columns: Column<VsphereObject>[] = [
    {
      id: "name",
      header: "Name",
      sortValue: (o) => o.name,
      cell: (o) => <span className="font-mono text-secondary">{o.name}</span>,
    },
    { id: "kind", header: "Kind", sortValue: (o) => o.kind, cell: (o) => kindLabel[o.kind] },
    { id: "vcenter", header: "vCenter", sortValue: (o) => o.vcenter, cell: (o) => o.vcenter },
    { id: "detail", header: "Capacity or reach", cell: (o) => <span className="text-secondary">{o.detail}</span> },
    {
      id: "tags",
      header: "Tags",
      sortValue: (o) => o.tags.join(" ") || null,
      cell: (o) =>
        o.tags.length ? (
          <span className="flex flex-wrap gap-1">
            {o.tags.map((t) => (
              <Badge key={t} tone="outline" className="font-mono">
                {t}
              </Badge>
            ))}
          </span>
        ) : (
          <span className="text-faint">None</span>
        ),
    },
  ];
  return (
    <Panel
      title="vCenter objects"
      description="The clusters, hosts, resource pools, datastores, datastore clusters and networks the last inventory sync read. Placement rules name them, and blueprints' constraints choose by their vSphere tags, written category/tag."
      flush
    >
      <DataTable
        label="vCenter objects"
        columns={columns}
        rows={rows}
        rowKey={(o) => `${o.kind}/${o.vcenter}/${o.cluster ?? ""}/${o.name}`}
        minWidth={760}
        defaultSort={{ id: "name", desc: false }}
        loading={inventory.isPending}
        error={inventory.isError && <ErrorState error={inventory.error} onRetry={() => void inventory.refetch()} />}
        toolbar={
          <>
            <TableSearch label="Search by name or tag" value={q} onChange={setQ} placeholder="Name or tag" />
            <Select
              aria-label="Kind"
              className="w-40"
              value={kind}
              onChange={(v) => setKind(v as Kind | "all")}
              options={[
                { value: "all", label: "All kinds" },
                { value: "cluster", label: "Clusters" },
                { value: "host", label: "Hosts" },
                { value: "resource_pool", label: "Resource pools" },
                { value: "datastore", label: "Datastores" },
                { value: "datastore_cluster", label: "Datastore clusters" },
                { value: "network", label: "Networks" },
              ]}
            />
          </>
        }
        empty={
          <EmptyState
            icon={Server}
            title={all?.length ? "Nothing matches" : "No vCenter objects yet"}
            body={
              all?.length
                ? "No vCenter object has that name or tag."
                : "The inventory sync lists them once a vCenter is connected."
            }
          />
        }
      />
    </Panel>
  );
}

export function InfrastructurePage() {
  const { me } = useCurrentProject();
  const canEdit = can(me, "infrastructure.manage");
  return (
    <Page>
      <PageHeader
        title="Templates and networks"
        description="What blueprints' template and size pickers offer, the subnet each network deploys into, and the tags of vCenter objects."
      />
      <Tabs defaultValue="templates">
        <TabsList aria-label="Infrastructure sections">
          <TabsTrigger value="templates">Templates</TabsTrigger>
          <TabsTrigger value="sizes">Sizes</TabsTrigger>
          <TabsTrigger value="networks">Networks</TabsTrigger>
          <TabsTrigger value="inventory">vCenter objects</TabsTrigger>
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
        <TabsContent value="inventory" className="pt-4">
          <Inventory />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
