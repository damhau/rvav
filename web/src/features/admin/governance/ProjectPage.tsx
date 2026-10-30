// One project's administration (S9, ADR-0020): details, members, placement
// with its preview, and quotas.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm, q } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { errorAt, failed, fieldErrors, td, th, UserPicker } from "./common";

type S = components["schemas"];
const roleOptions = [
  { value: "project-admin", label: "Project admin" },
  { value: "member", label: "Member" },
  { value: "viewer", label: "Viewer" },
];
const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

function Details({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const qc = useQueryClient();
  const envs = useQuery(q.environments());
  const [f, setF] = useState({
    name: project.name,
    description: project.description ?? "",
    cost_center: project.cost_center ?? "",
    environments: project.environments,
  });
  const save = useMutation({
    mutationFn: (body: S["AdminProjectUpdate"]) =>
      call(api.PATCH("/admin/projects/{projectId}", { params: { path: { projectId: project.id } }, body })),
    onSuccess: (p, body) => {
      void qc.invalidateQueries({ queryKey: ["admin"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
      toast.success(
        body.archived === true
          ? `${p.name} archived`
          : body.archived === false
            ? `${p.name} back in use`
            : `${p.name} saved`,
      );
    },
    onError: (e) => failed(e, "Not saved"),
  });
  return (
    <Panel title="Details">
      <form
        className="grid max-w-2xl grid-cols-1 gap-3 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(f);
        }}
      >
        <Field label="Name" htmlFor="p-name">
          <Input
            id="p-name"
            value={f.name}
            disabled={!isAdmin}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label="Cost centre" htmlFor="p-cost">
          <Input
            id="p-cost"
            value={f.cost_center}
            disabled={!isAdmin}
            onChange={(e) => setF({ ...f, cost_center: e.target.value })}
          />
        </Field>
        <Field label="Description" htmlFor="p-description" className="md:col-span-2">
          <Input
            id="p-description"
            value={f.description}
            disabled={!isAdmin}
            onChange={(e) => setF({ ...f, description: e.target.value })}
          />
        </Field>
        <fieldset className="flex flex-wrap gap-3 md:col-span-2">
          <legend className="mb-1 text-xs font-medium">Environments it deploys to</legend>
          {(envs.data?.items ?? []).map((e) => (
            <label key={e.key} htmlFor={`p-env-${e.key}`} className="flex items-center gap-1.5 text-[13px]">
              <Checkbox
                id={`p-env-${e.key}`}
                disabled={!isAdmin}
                checked={f.environments.includes(e.key)}
                onChange={(on) =>
                  setF({
                    ...f,
                    environments: on ? [...f.environments, e.key] : f.environments.filter((x) => x !== e.key),
                  })
                }
              />
              {e.name}
            </label>
          ))}
        </fieldset>
        {isAdmin && (
          <div className="flex gap-2 md:col-span-2">
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save details
            </Button>
            <Button
              type="button"
              variant={project.archived ? "secondary" : "danger"}
              onClick={() =>
                (project.archived ||
                  window.confirm(`Archive ${project.name}? It can't request anything new; its deployments go on.`)) &&
                save.mutate({ archived: !project.archived })
              }
            >
              {project.archived ? "Put back in use" : "Archive"}
            </Button>
          </div>
        )}
      </form>
    </Panel>
  );
}

function Members({ projectId, canManage }: { projectId: string; canManage: boolean }) {
  const qc = useQueryClient();
  const members = useQuery(adm.members(projectId));
  const [role, setRole] = useState("member");
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "members", projectId] });
    void qc.invalidateQueries({ queryKey: ["admin", "project", projectId] });
  };
  const set = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      call(
        api.PUT("/projects/{projectId}/members/{userId}", {
          params: { path: { projectId, userId } },
          body: { role: role as S["ProjectRole"] },
        }),
      ),
    onSuccess: (m) => {
      refresh();
      toast.success(`${m.user.name} is a ${m.direct_role ?? m.role}`);
    },
    onError: (e) => failed(e, "Not changed"),
  });
  const remove = useMutation({
    mutationFn: (userId: string) =>
      call(api.DELETE("/projects/{projectId}/members/{userId}", { params: { path: { projectId, userId } } })),
    onSuccess: () => {
      refresh();
      toast.success("Member removed");
    },
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel title="Members" flush>
      {canManage && (
        <div className="flex flex-wrap items-end gap-2 border-b border-line px-4 py-3">
          <div className="w-72">
            <UserPicker
              label="Add someone"
              id="member-search"
              onPick={(u) =>
                // Picking someone already in the project doesn't change their role: the list does.
                members.data?.items.some((m) => m.user.id === u.id && m.direct)
                  ? toast.info(`${u.name} is already a member`, { description: "Change their role in the list." })
                  : set.mutate({ userId: u.id, role })
              }
            />
          </div>
          <Field label="As" htmlFor="member-role" className="w-40">
            <Select id="member-role" value={role} onChange={setRole} options={roleOptions} />
          </Field>
        </div>
      )}
      {members.isPending && <SkeletonRows rows={3} cols={3} />}
      {members.isError && <ErrorState error={members.error} onRetry={() => void members.refetch()} />}
      {members.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Who
                </th>
                <th scope="col" className={th}>
                  Role
                </th>
                <th scope="col" className={th}>
                  Through
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {members.data.items.map((m) => (
                <tr key={m.user.id}>
                  <td className={`${td} pl-4`}>
                    {m.user.name} <span className="text-xs text-faint">{m.user.email}</span>
                  </td>
                  <td className={td}>
                    {m.direct && canManage ? (
                      <Select
                        aria-label={`Role of ${m.user.name}`}
                        value={m.direct_role ?? m.role}
                        onChange={(v) => set.mutate({ userId: m.user.id, role: v })}
                        options={roleOptions}
                        className="w-40"
                      />
                    ) : (
                      roleOptions.find((r) => r.value === (m.direct_role ?? m.role))?.label
                    )}
                  </td>
                  <td className={`${td} text-xs text-muted`}>
                    {[m.direct ? "Direct" : "", ...m.groups.map((g) => `group ${g}`)].filter(Boolean).join(", ")}
                  </td>
                  <td className={`${td} text-right`}>
                    {m.direct && canManage && (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Remove ${m.user.name}`}
                        onClick={() => remove.mutate(m.user.id)}
                      >
                        Remove
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function RuleDialog({
  project,
  env,
  rule,
  onClose,
}: {
  project: S["AdminProject"];
  env: string;
  rule?: S["PlacementRule"];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [f, setF] = useState({
    clusters: rule?.clusters.join(", ") ?? `GVA-${env.toUpperCase()}-*`,
    cluster_tags: rule?.cluster_tags.join(", ") ?? "",
    datastores: rule?.datastores.join(", ") ?? "*",
    datastore_tags: rule?.datastore_tags.join(", ") ?? "",
    networks: rule?.networks.join(", ") ?? "",
    folder: rule?.folder ?? `/GVA/vm/rvav/${project.code}/${env}`,
    storage_policy: rule?.storage_policy ?? "",
    vcenter: rule?.vcenter ?? "",
    priority: String(rule?.priority ?? 100),
  });
  // NSX segments made for deployments (ADR-0028).
  const [segOn, setSegOn] = useState(Boolean(rule?.segments));
  const [seg, setSeg] = useState({
    tier1: rule?.segments?.tier1 ?? "",
    ip_block: rule?.segments?.ip_block ?? "",
    prefix_length: String(rule?.segments?.prefix_length ?? 26),
    dns_zone: rule?.segments?.dns_zone ?? "",
    dns_servers: rule?.segments?.dns_servers?.join(", ") ?? "",
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body: S["PlacementRuleWrite"] = {
        project_id: project.id,
        environment: env,
        priority: f.priority.trim() === "" ? 100 : Number(f.priority),
        clusters: list(f.clusters),
        cluster_tags: list(f.cluster_tags),
        datastores: list(f.datastores),
        datastore_tags: list(f.datastore_tags),
        networks: list(f.networks),
        folder: f.folder,
        storage_policy: f.storage_policy.trim(),
        vcenter: f.vcenter.trim(),
        segments: segOn
          ? {
              tier1: seg.tier1.trim(),
              ip_block: seg.ip_block.trim(),
              prefix_length: Number(seg.prefix_length),
              dns_zone: seg.dns_zone.trim(),
              dns_servers: list(seg.dns_servers),
            }
          : null,
      };
      return rule
        ? call(api.PUT("/admin/placement-rules/{id}", { params: { path: { id: rule.id } }, body }))
        : call(api.POST("/admin/placement-rules", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "placement-rules"] });
      void qc.invalidateQueries({ queryKey: ["admin", "placement-preview"] });
      toast.success("Placement rule saved");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const field = (k: keyof typeof f, label: string, hint?: string, mono = true) => (
    <Field label={label} htmlFor={`rule-${k}`} error={errorAt(errors, `/${k}`)} hint={hint}>
      <Input
        id={`rule-${k}`}
        className={mono ? "font-mono text-xs" : undefined}
        value={f[k]}
        onChange={(e) => setF({ ...f, [k]: e.target.value })}
      />
    </Field>
  );
  const segField = (k: keyof typeof seg, label: string, hint?: string) => (
    <Field label={label} htmlFor={`rule-seg-${k}`} error={errorAt(errors, `/segments/${k}`)} hint={hint}>
      <Input
        id={`rule-seg-${k}`}
        className="font-mono text-xs"
        value={seg[k]}
        onChange={(e) => setSeg({ ...seg, [k]: e.target.value })}
      />
    </Field>
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={`${rule ? "Change" : "New"} placement rule for ${project.name} in ${env}`}
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
          <p className="text-xs text-muted">
            Names take * patterns and are comma-separated; tags are category/tag, all of them needed.
          </p>
          <div className="grid grid-cols-2 gap-3">
            {field("clusters", "Clusters")}
            {field("cluster_tags", "Cluster tags", "For example rvav-tier/gold")}
            {field("datastores", "Datastores")}
            {field("datastore_tags", "Datastore tags")}
            {field("networks", "Networks", "For example dvpg-*-dev-*")}
            {field("folder", "VM folder")}
            {field("storage_policy", "Storage policy", "Empty for the datastore's default", false)}
            {field("vcenter", "vCenter", "Empty for any; needed when a cluster's name is in two")}
            {field("priority", "Priority", "Lower first", false)}
          </div>
          <label htmlFor="rule-seg-on" className="flex items-center gap-2 text-[13px]">
            <Checkbox id="rule-seg-on" checked={segOn} onChange={setSegOn} />
            NSX segments made for deployments
          </label>
          {segOn && (
            <fieldset className="grid grid-cols-2 gap-3 rounded-md border border-line p-3">
              <legend className="px-1 text-xs text-muted">
                Items that make their own networks get segments on this Tier-1, their subnets from this block
              </legend>
              {segField("tier1", "Tier-1 gateway", "Its NSX ID, for example t1-gva-dev")}
              {segField("ip_block", "EfficientIP block", "Its name or range")}
              {segField("prefix_length", "Subnet size", "Prefix length, 16 to 29")}
              {segField("dns_zone", "DNS zone")}
              {segField("dns_servers", "DNS servers", "Comma-separated")}
            </fieldset>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save rule
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Placement({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const qc = useQueryClient();
  const [env, setEnv] = useState(project.environments[0] ?? "dev");
  const [editing, setEditing] = useState<S["PlacementRule"] | "new" | null>(null);
  const rules = useQuery(adm.placementRules(project.id));
  const preview = useQuery(adm.placementPreview(project.id, env));
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/placement-rules/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "placement-rules"] });
      void qc.invalidateQueries({ queryKey: ["admin", "placement-preview"] });
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const here = (rules.data?.items ?? []).filter((r) => r.environment === env);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-end gap-3">
        <Field label="Environment" htmlFor="placement-env" className="w-48">
          <Select
            id="placement-env"
            value={env}
            onChange={setEnv}
            options={project.environments.map((e) => ({ value: e, label: e }))}
          />
        </Field>
      </div>
      <Panel
        title="Rules"
        flush
        actions={
          isAdmin && (
            <Button size="sm" onClick={() => setEditing("new")}>
              New rule
            </Button>
          )
        }
      >
        {here.length === 0 && (
          <EmptyState title="No rule" body={`Requests of ${project.name} in ${env} have nowhere to go.`} />
        )}
        {here.map((r) => (
          <div
            key={r.id}
            className="flex flex-wrap items-start justify-between gap-2 border-b border-line px-4 py-2 text-xs last:border-b-0"
          >
            <dl className="grid grid-cols-[110px_1fr] gap-x-3 gap-y-0.5">
              <dt className="text-faint">Clusters</dt>
              <dd className="font-mono">{[...r.clusters, ...r.cluster_tags.map((t) => `tag ${t}`)].join(", ")}</dd>
              <dt className="text-faint">Datastores</dt>
              <dd className="font-mono">{[...r.datastores, ...r.datastore_tags.map((t) => `tag ${t}`)].join(", ")}</dd>
              <dt className="text-faint">Networks</dt>
              <dd className="font-mono">{r.networks.join(", ")}</dd>
              <dt className="text-faint">Folder</dt>
              <dd className="font-mono">{r.folder}</dd>
              {r.storage_policy && (
                <>
                  <dt className="text-faint">Storage policy</dt>
                  <dd>{r.storage_policy}</dd>
                </>
              )}
              {r.vcenter && (
                <>
                  <dt className="text-faint">vCenter</dt>
                  <dd className="font-mono">{r.vcenter}</dd>
                </>
              )}
              {r.segments && (
                <>
                  <dt className="text-faint">NSX segments</dt>
                  <dd className="font-mono">
                    {r.segments.tier1} · {r.segments.ip_block} /{r.segments.prefix_length} · {r.segments.dns_zone}
                  </dd>
                </>
              )}
            </dl>
            {isAdmin && (
              <div className="flex gap-1">
                <Button size="sm" variant="ghost" onClick={() => setEditing(r)}>
                  Change
                </Button>
                <Button size="sm" variant="ghost" aria-label="Remove the rule" onClick={() => remove.mutate(r.id)}>
                  Remove
                </Button>
              </div>
            )}
          </div>
        ))}
      </Panel>
      <Panel title={`What ${project.name} gets in ${env}`}>
        {preview.isPending && <Skeleton className="h-24" />}
        {preview.data && (
          <div className="flex flex-col gap-2 text-xs">
            {preview.data.problems.length > 0 && (
              <ul aria-label="What stops a request here" className="flex flex-col gap-0.5 text-warn">
                {preview.data.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            )}
            <p>
              <span className="text-faint">Clusters:</span>{" "}
              {preview.data.clusters.map((c) => c.name).join(", ") || "none"}
            </p>
            <p>
              <span className="text-faint">Datastores:</span>{" "}
              {preview.data.datastores.map((d) => `${d.name} (${d.free_gb} GB free)`).join(", ") || "none"}
            </p>
            <p>
              <span className="text-faint">Networks:</span>{" "}
              {preview.data.networks.map((n) => `${n.name}${n.subnet ? ` ${n.subnet}` : " (no subnet)"}`).join(", ") ||
                "none"}
            </p>
            <p>
              <span className="text-faint">Folder:</span>{" "}
              <span className="font-mono">{preview.data.folder || "—"}</span>
            </p>
          </div>
        )}
      </Panel>
      {editing && (
        <RuleDialog
          project={project}
          env={env}
          rule={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

const resources = [
  { value: "vcpu", label: "vCPU" },
  { value: "memory_gb", label: "Memory (GB)" },
  { value: "storage_gb", label: "Storage (GB)" },
  { value: "vm_count", label: "VMs" },
];

function Quotas({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const qc = useQueryClient();
  const quotas = useQuery(adm.quotas(project.id));
  const [rows, setRows] = useState<S["QuotaLimit"][] | null>(null);
  // Saving replaces every limit: nothing is offered until the current ones are in.
  const shown = rows ?? quotas.data?.items ?? [];
  const ready = Boolean(quotas.data);
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/projects/{projectId}/quotas", {
          params: { path: { projectId: project.id } },
          body: { items: shown },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "quotas", project.id] });
      setRows(null);
      toast.success("Quotas saved");
    },
    onError: (e) => failed(e, "Not saved"),
  });
  const set = (i: number, row: S["QuotaLimit"]) => setRows(shown.map((r, j) => (j === i ? row : r)));
  return (
    <Panel
      title="Quotas"
      flush
      actions={
        isAdmin && (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="ghost"
              disabled={!ready}
              onClick={() =>
                setRows([...shown, { environment: "", resource: "vcpu", limit: null, enforcement: "hard" }])
              }
            >
              Add a limit
            </Button>
            <Button
              size="sm"
              variant="primary"
              disabled={!ready || !rows || save.isPending}
              onClick={() => save.mutate()}
            >
              Save quotas
            </Button>
          </div>
        )
      }
    >
      {quotas.isPending && <SkeletonRows rows={3} cols={4} />}
      {quotas.isError && <ErrorState error={quotas.error} onRetry={() => void quotas.refetch()} />}
      {ready && shown.length === 0 && <p className="px-4 py-3 text-xs text-muted">No limit.</p>}
      {ready && shown.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Resource
                </th>
                <th scope="col" className={th}>
                  In
                </th>
                <th scope="col" className={th}>
                  Limit
                </th>
                <th scope="col" className={th}>
                  When passed
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r, i) => (
                <tr key={i}>
                  <td className={`${td} pl-4`}>
                    <Select
                      aria-label={`Limit ${i + 1}: resource`}
                      value={r.resource}
                      onChange={(v) => set(i, { ...r, resource: v as S["QuotaResource"] })}
                      options={resources}
                      disabled={!isAdmin}
                      className="w-40"
                    />
                  </td>
                  <td className={td}>
                    <Select
                      aria-label={`Limit ${i + 1}: environment`}
                      value={r.environment || "all"}
                      onChange={(v) => set(i, { ...r, environment: v === "all" ? "" : v })}
                      options={[
                        { value: "all", label: "The whole project" },
                        ...project.environments.map((e) => ({ value: e, label: e })),
                      ]}
                      disabled={!isAdmin}
                      className="w-44"
                    />
                  </td>
                  <td className={td}>
                    <Input
                      aria-label={`Limit ${i + 1}: value`}
                      type="number"
                      min={0}
                      className="h-8 w-24"
                      placeholder="None"
                      disabled={!isAdmin}
                      value={r.limit ?? ""}
                      onChange={(e) => set(i, { ...r, limit: e.target.value === "" ? null : Number(e.target.value) })}
                    />
                  </td>
                  <td className={td}>
                    <Select
                      aria-label={`Limit ${i + 1}: enforcement`}
                      value={r.enforcement}
                      onChange={(v) => set(i, { ...r, enforcement: v as S["QuotaLimit"]["enforcement"] })}
                      options={[
                        { value: "hard", label: "Refused" },
                        { value: "soft", label: "Needs approval" },
                      ]}
                      disabled={!isAdmin}
                      className="w-40"
                    />
                  </td>
                  <td className={`${td} text-right`}>
                    {isAdmin && (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Remove limit ${i + 1}`}
                        onClick={() => setRows(shown.filter((_, j) => j !== i))}
                      >
                        Remove
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

export function ProjectPage({ projectId }: { projectId: string }) {
  const { me } = useCurrentProject();
  const isAdmin = me.global_roles.includes("admin");
  const project = useQuery(adm.project(projectId));
  if (project.isPending)
    return (
      <Page>
        <Skeleton className="h-64" />
      </Page>
    );
  if (project.isError)
    return (
      <Page>
        <ErrorState error={project.error} onRetry={() => void project.refetch()} />
      </Page>
    );
  const p = project.data;
  return (
    <Page>
      <PageHeader
        crumbs={
          <Link to="/admin/projects" className="hover:text-fg">
            Projects
          </Link>
        }
        title={p.name}
        meta={
          <>
            <span className="font-mono">{p.code}</span>
            <span>{p.members} members</span>
            <span>{p.deployments} deployments</span>
            {p.archived && <Badge tone="neutral">Archived</Badge>}
          </>
        }
      />
      <Tabs defaultValue="members">
        <TabsList aria-label="Project sections">
          <TabsTrigger value="members">Members</TabsTrigger>
          <TabsTrigger value="placement">Placement</TabsTrigger>
          <TabsTrigger value="quotas">Quotas</TabsTrigger>
          <TabsTrigger value="details">Details</TabsTrigger>
        </TabsList>
        <TabsContent value="members" className="pt-4">
          <Members
            projectId={p.id}
            canManage={isAdmin || me.projects.some((x) => x.project.id === p.id && x.role === "project-admin")}
          />
        </TabsContent>
        <TabsContent value="placement" className="pt-4">
          <Placement project={p} isAdmin={isAdmin} />
        </TabsContent>
        <TabsContent value="quotas" className="pt-4">
          <Quotas project={p} isAdmin={isAdmin} />
        </TabsContent>
        <TabsContent value="details" className="pt-4">
          <Details
            key={[p.name, p.description, p.cost_center, p.environments.join(), p.archived].join("|")}
            project={p}
            isAdmin={isAdmin}
          />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
