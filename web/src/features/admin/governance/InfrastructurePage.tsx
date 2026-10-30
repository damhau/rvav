// VM templates, size presets and network mappings (S9, ADR-0020): what
// blueprints' template and size pickers offer, and the subnet each vCenter
// network deploys into.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, OfferEditor, offersText, td, th, writable } from "./common";

type S = components["schemas"];

function useProjectName() {
  const projects = useQuery(adm.projects("", false));
  return (id: string) => projects.data?.items.find((p) => p.id === id)?.name ?? id;
}

function TemplateDialog({ template, onClose }: { template?: S["VmTemplate"]; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<S["VmTemplateWrite"]>(
    template
      ? writable(template, "in_inventory")
      : {
          key: "",
          label: "",
          vcenter_template: "",
          content_library: "",
          os_family: "linux",
          customization: "",
          description: "",
          offers: [{ project_id: null, environment: null }],
        },
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      template
        ? call(api.PUT("/admin/templates/{key}", { params: { path: { key: template.key } }, body: f }))
        : call(api.POST("/admin/templates", { body: f })),
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
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={template ? `Change ${template.label}` : "New VM template"}
        className="w-[min(620px,calc(100vw-32px))]"
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-[120px_1fr] gap-3">
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
          <div className="grid grid-cols-2 gap-3">
            <Field label="vCenter template" htmlFor="tpl-vc" error={errors["/vcenter_template"]}>
              <Input
                id="tpl-vc"
                className="font-mono text-xs"
                value={f.vcenter_template}
                onChange={(e) => setF({ ...f, vcenter_template: e.target.value })}
              />
            </Field>
            <Field
              label="Content library"
              htmlFor="tpl-lib"
              hint="When the template is an item of one"
              error={errors["/content_library"]}
            >
              <Input
                id="tpl-lib"
                className="font-mono text-xs"
                value={f.content_library ?? ""}
                onChange={(e) => setF({ ...f, content_library: e.target.value })}
              />
            </Field>
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
          <OfferEditor
            value={f.offers ?? []}
            onChange={(offers) => setF({ ...f, offers })}
            error={errorAt(errors, "/offers")}
          />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!f.key || save.isPending}>
              Save template
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Templates({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const templates = useQuery(adm.templates());
  const projectName = useProjectName();
  const [editing, setEditing] = useState<S["VmTemplate"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/templates/{key}", { params: { path: { key } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "templates"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="VM templates"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New template
          </Button>
        )
      }
    >
      {templates.isPending && <SkeletonRows rows={2} cols={4} />}
      {templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
      {templates.data && templates.data.items.length === 0 && (
        <EmptyState title="No template" body="Blueprints name a vCenter template themselves." />
      )}
      {templates.data && templates.data.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Template
                </th>
                <th scope="col" className={th}>
                  In vCenter
                </th>
                <th scope="col" className={th}>
                  Offered to
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {templates.data.items.map((t) => (
                <tr key={t.key}>
                  <td className={`${td} pl-4`}>
                    <span className="font-medium">{t.label}</span>{" "}
                    <span className="font-mono text-[11px] text-faint">{t.key}</span>
                  </td>
                  <td className={`${td} font-mono text-xs`}>
                    {t.content_library && <span className="text-faint">{t.content_library} / </span>}
                    {t.vcenter_template}
                    {!t.in_inventory && (
                      <Badge tone="warn" className="ml-2">
                        Not found
                      </Badge>
                    )}
                  </td>
                  <td className={`${td} text-xs text-muted`}>{offersText(t.offers, projectName)}</td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {canEdit && (
                      <>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Change ${t.label}`}
                          onClick={() => setEditing(t)}
                        >
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${t.label}`}
                          onClick={() => remove.mutate(t.key)}
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
      {editing && (
        <TemplateDialog template={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />
      )}
    </Panel>
  );
}

function SizeDialog({ size, onClose }: { size?: S["SizePreset"]; onClose: () => void }) {
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
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={size ? `Change ${size.label}` : "New size"} className="w-[min(620px,calc(100vw-32px))]">
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-[120px_1fr] gap-3">
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
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!f.key || save.isPending}>
              Save size
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Sizes({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const sizes = useQuery(adm.sizes());
  const projectName = useProjectName();
  const [editing, setEditing] = useState<S["SizePreset"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/sizes/{key}", { params: { path: { key } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "sizes"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="Sizes"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New size
          </Button>
        )
      }
    >
      {sizes.isPending && <SkeletonRows rows={3} cols={4} />}
      {sizes.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Size
                </th>
                <th scope="col" className={th}>
                  vCPU, memory, disk
                </th>
                <th scope="col" className={th}>
                  Offered to
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {sizes.data.items.map((z) => (
                <tr key={z.key}>
                  <td className={`${td} pl-4`}>
                    <span className="font-medium">{z.label}</span>{" "}
                    <span className="font-mono text-[11px] text-faint">{z.key}</span>
                  </td>
                  <td className={`${td} text-xs`}>
                    {z.vcpu} vCPU, {z.memory_gb} GB, {z.disk_gb} GB
                  </td>
                  <td className={`${td} text-xs text-muted`}>{offersText(z.offers, projectName)}</td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {canEdit && (
                      <>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Change ${z.label}`}
                          onClick={() => setEditing(z)}
                        >
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${z.label}`}
                          onClick={() => remove.mutate(z.key)}
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
      {editing && <SizeDialog size={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

function MappingDialog({ mapping, onClose }: { mapping?: S["NetworkMapping"]; onClose: () => void }) {
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
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={mapping ? `Map ${mapping.network} by hand` : "Map a network"}
        description="A mapping by hand wins over EfficientIP's until it is removed."
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <Field label="vCenter network" htmlFor="map-network">
            <Input
              id="map-network"
              className="font-mono text-xs"
              disabled={Boolean(mapping)}
              value={network}
              onChange={(e) => setNetwork(e.target.value)}
            />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Subnet" htmlFor="map-subnet" error={errors["/subnet"]}>
              <Input
                id="map-subnet"
                className="font-mono text-xs"
                value={f.subnet}
                onChange={(e) => setF({ ...f, subnet: e.target.value })}
              />
            </Field>
            <Field label="Gateway" htmlFor="map-gateway" error={errors["/gateway"]}>
              <Input
                id="map-gateway"
                className="font-mono text-xs"
                value={f.gateway}
                onChange={(e) => setF({ ...f, gateway: e.target.value })}
              />
            </Field>
            <Field label="DNS zone" htmlFor="map-zone" error={errors["/dns_zone"]}>
              <Input id="map-zone" value={f.dns_zone} onChange={(e) => setF({ ...f, dns_zone: e.target.value })} />
            </Field>
            <Field label="DNS servers" htmlFor="map-dns" hint="Comma-separated">
              <Input
                id="map-dns"
                className="font-mono text-xs"
                value={f.dns_servers}
                onChange={(e) => setF({ ...f, dns_servers: e.target.value })}
              />
            </Field>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!network || save.isPending}>
              Save mapping
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Networks({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const mappings = useQuery(adm.networkMappings());
  const [editing, setEditing] = useState<S["NetworkMapping"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (network: string) =>
      call(api.DELETE("/admin/network-mappings/{network}", { params: { path: { network } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "network-mappings"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="Network mappings"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            Map a network
          </Button>
        )
      }
    >
      <p className="border-b border-line px-4 py-2 text-xs text-muted">
        EfficientIP's subnets come with their vCenter network in the sync; map a network by hand when EfficientIP
        doesn't say.
      </p>
      {mappings.isPending && <SkeletonRows rows={3} cols={4} />}
      {mappings.data && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Network
                </th>
                <th scope="col" className={th}>
                  Subnet
                </th>
                <th scope="col" className={th}>
                  DNS
                </th>
                <th scope="col" className={th}>
                  From
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {mappings.data.items.map((m) => (
                <tr key={m.network}>
                  <td className={`${td} pl-4 font-mono text-xs`}>{m.network}</td>
                  <td className={`${td} font-mono text-xs`}>
                    {m.subnet} <span className="text-faint">via {m.gateway}</span>
                  </td>
                  <td className={`${td} text-xs`}>
                    {m.dns_zone} <span className="text-faint">{m.dns_servers.join(", ")}</span>
                  </td>
                  <td className={td}>
                    <Badge tone={m.source === "manual" ? "accent" : "neutral"}>
                      {m.source === "manual" ? "By hand" : m.source === "nsx" ? "NSX segment" : "EfficientIP"}
                    </Badge>
                  </td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {/* A segment made for a deployment goes with it (ADR-0028). */}
                    {canEdit && m.source !== "nsx" && (
                      <>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Change: map ${m.network} by hand`}
                          onClick={() => setEditing(m)}
                        >
                          Change
                        </Button>
                        {m.source === "manual" && (
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Remove the mapping of ${m.network}`}
                            onClick={() => remove.mutate(m.network)}
                          >
                            Remove
                          </Button>
                        )}
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <MappingDialog mapping={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

export function InfrastructurePage() {
  const { me } = useCurrentProject();
  const canEdit = me.global_roles.includes("admin");
  return (
    <Page>
      <PageHeader
        title="Templates, sizes and networks"
        meta="What blueprints' template and size pickers offer, and the subnet each network deploys into."
      />
      <Tabs defaultValue="templates">
        <TabsList aria-label="Infrastructure sections">
          <TabsTrigger value="templates">Templates</TabsTrigger>
          <TabsTrigger value="sizes">Sizes</TabsTrigger>
          <TabsTrigger value="networks">Networks</TabsTrigger>
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
      </Tabs>
    </Page>
  );
}
