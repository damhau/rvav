// One project's administration (S9, ADR-0020): details, members, placement
// with its preview, and quotas.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Gauge, Route as RouteIcon, UserRound } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm, q } from "@/lib/query";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { cn } from "@/lib/utils";
import { Refusal, refused } from "../access/refusal";
import { errorAt, failed, fieldErrors, UserPicker } from "./common";
import { ArchiveDialog, quotaTone, takenText } from "./ProjectsPage";

type S = components["schemas"];

export const projectTabs = ["members", "placement", "quotas", "details"] as const;
export type ProjectTab = (typeof projectTabs)[number];

const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

function Details({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const qc = useQueryClient();
  const envs = useQuery(q.environments());
  const [archiving, setArchiving] = useState(false);
  const [f, setF] = useState({
    name: project.name,
    description: project.description ?? "",
    cost_center: project.cost_center ?? "",
    environments: project.environments,
  });
  const save = useMutation({
    mutationFn: (body: S["AdminProjectUpdate"]) =>
      call(api.PATCH("/admin/projects/{projectId}", { params: { path: { projectId: project.id } }, body })),
    onSuccess: (p) => {
      void qc.invalidateQueries({ queryKey: ["admin"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
      toast.success(`${p.name} saved`);
    },
    onError: (e) => failed(e, "Not saved"),
  });
  return (
    <Panel
      title="Details"
      footer={
        isAdmin && (
          <>
            <Button variant={project.archived ? "secondary" : "danger"} onClick={() => setArchiving(true)}>
              {project.archived ? "Put back in use" : "Archive"}
            </Button>
            <Button type="submit" form="project-details" variant="primary" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save details"}
            </Button>
          </>
        )
      }
    >
      <form
        id="project-details"
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
          <legend className="mb-1 text-secondary font-medium">Environments it deploys to</legend>
          {(envs.data?.items ?? []).map((e) => (
            <label key={e.key} htmlFor={`p-env-${e.key}`} className="flex items-center gap-1.5 text-body">
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
      </form>
      {archiving && <ArchiveDialog project={project} onClose={() => setArchiving(false)} />}
    </Panel>
  );
}

/** Where a role a member holds here comes from. */
function sourceText(r: S["ProjectMemberRole"]): string {
  return [r.direct ? "given here" : "", ...r.groups.map((g) => `through ${g}`)].filter(Boolean).join(" and ");
}

/** The roles given to someone in the project themselves: those the caller may give, and those held already. */
function RolesSheet({
  project,
  member,
  offered,
  onClose,
}: {
  project: S["AdminProject"];
  member: S["ProjectMember"];
  offered: S["AssignableRole"][];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [keys, setKeys] = useState(member.direct_roles);
  const kept = member.roles.filter((r) => r.direct && !offered.some((o) => o.key === r.key));
  const viaGroups = member.roles.filter((r) => r.groups.length > 0);
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/projects/{projectId}/members/{userId}", {
          params: { path: { projectId: project.id, userId: member.user.id } },
          body: { roles: keys },
        }),
      ),
    onSuccess: (m) => {
      void qc.invalidateQueries({ queryKey: ["admin"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
      toast.success(`${m.user.name}'s roles saved`);
      onClose();
    },
    onError: (e) => {
      if (!refused(e)) failed(e, "Not saved");
    },
  });
  const toggle = (key: string, on: boolean) => setKeys(on ? [...keys, key] : keys.filter((k) => k !== key));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Roles of ${member.user.name}`}
      description={`Given to them in ${project.name} themselves. A role through a group changes with its mapping.`}
      submitLabel={save.isPending ? "Saving…" : "Save roles"}
      submitDisabled={keys.length === 0 || save.isPending}
      onSubmit={() => save.mutate()}
    >
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1 text-secondary font-medium">Given here</legend>
        {offered.map((r) => (
          <label key={r.key} htmlFor={`mr-${r.key}`} className="flex items-center gap-2 text-body">
            <Checkbox id={`mr-${r.key}`} checked={keys.includes(r.key)} onChange={(on) => toggle(r.key, on)} />
            {r.name}
            {!r.built_in && <span className="font-mono text-secondary text-muted">{r.key}</span>}
          </label>
        ))}
        {kept.map((r) => (
          <label key={r.key} htmlFor={`mr-${r.key}`} className="flex items-center gap-2 text-body">
            <Checkbox id={`mr-${r.key}`} checked disabled onChange={() => undefined} />
            {r.name}
            <span className="text-secondary text-muted">(you may not take it away)</span>
          </label>
        ))}
      </fieldset>
      {viaGroups.length > 0 && (
        <p className="text-secondary text-muted">
          Through groups: {viaGroups.map((r) => `${r.name} (${r.groups.join(", ")})`).join("; ")}.
        </p>
      )}
      {keys.length === 0 && (
        <p className="text-secondary text-muted">Choose a role, or remove them from the project in the list.</p>
      )}
      <Refusal error={save.error} />
    </Sheet>
  );
}

function Members({ project, canManage }: { project: S["AdminProject"]; canManage: boolean }) {
  const projectId = project.id;
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const members = useQuery(adm.members(projectId));
  const offered = useQuery({ ...adm.assignableRoles(projectId), enabled: canManage });
  const [picked, setRole] = useState("member");
  const [editing, setEditing] = useState<S["ProjectMember"] | null>(null);
  const [removing, setRemoving] = useState<S["ProjectMember"] | null>(null);
  // Member unless the caller may not give it: then the first role they may.
  const items = offered.data?.items ?? [];
  const role = items.some((r) => r.key === picked)
    ? picked
    : ((items.find((r) => r.key === "member") ?? items[0])?.key ?? picked);
  const roleName = (key: string) => offered.data?.items.find((r) => r.key === key)?.name ?? key;
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin"] });
    void qc.invalidateQueries({ queryKey: ["me"] });
  };
  const add = useMutation({
    mutationFn: ({ userId, roles }: { userId: string; roles: string[] }) =>
      call(
        api.PUT("/projects/{projectId}/members/{userId}", {
          params: { path: { projectId, userId } },
          body: { roles },
        }),
      ),
    onSuccess: (m) => {
      refresh();
      toast.success(`${m.user.name} added as ${roleName(role)}`);
    },
    onError: (e) => {
      if (!refused(e)) failed(e, "Not added");
    },
  });
  const remove = useMutation({
    mutationFn: (m: S["ProjectMember"]) =>
      call(
        api.DELETE("/projects/{projectId}/members/{userId}", { params: { path: { projectId, userId: m.user.id } } }),
      ),
    onSuccess: (_, m) => {
      refresh();
      toast.success(`${m.user.name} removed`);
      setRemoving(null);
    },
    onError: (e) => {
      setRemoving(null);
      if (!refused(e)) failed(e, "Not removed");
    },
  });
  const seesRoles = can(me, "access.read");
  // The server refuses to take away a role the caller may not give: those not offered.
  const removable = (m: S["ProjectMember"]) =>
    m.direct && m.roles.every((r) => !r.direct || offered.data?.items.some((o) => o.key === r.key));
  const columns: Column<S["ProjectMember"]>[] = [
    {
      id: "who",
      header: "Who",
      sortValue: (m) => m.user.name.toLowerCase(),
      cell: (m) => (
        <div className="flex flex-col">
          <span className="font-medium">{m.user.name}</span>
          <span className="text-secondary text-faint">{m.user.email}</span>
        </div>
      ),
    },
    {
      id: "roles",
      header: "Roles, and where each comes from",
      cell: (m) => (
        <ul aria-label={`Roles of ${m.user.name}`} className="flex flex-col gap-0.5">
          {m.roles.map((r) => (
            <li key={r.key}>
              {seesRoles ? (
                <Link
                  to="/admin/roles/$roleKey"
                  params={{ roleKey: r.key }}
                  className="text-accent underline underline-offset-2"
                >
                  {r.name}
                </Link>
              ) : (
                r.name
              )}{" "}
              <span className="text-secondary text-muted">{sourceText(r)}</span>
            </li>
          ))}
        </ul>
      ),
    },
  ];
  const kept = (m: S["ProjectMember"]) => m.roles.filter((r) => r.groups.length > 0);
  return (
    <Panel title="Members" flush>
      {refused(remove.error) && (
        <div className="border-b border-line px-4 py-3">
          <Refusal error={remove.error} />
        </div>
      )}
      <DataTable
        label="Members"
        columns={columns}
        rows={members.data?.items}
        rowKey={(m) => m.user.id}
        minWidth={560}
        loading={members.isPending}
        error={members.isError && <ErrorState error={members.error} onRetry={() => void members.refetch()} />}
        toolbar={
          canManage && (
            <div className="flex w-full flex-col gap-2">
              <div className="flex flex-wrap items-end gap-2">
                <div className="w-72">
                  <UserPicker
                    label="Add someone"
                    id="member-search"
                    onPick={(u) => {
                      const m = members.data?.items.find((x) => x.user.id === u.id);
                      if (m?.direct_roles.includes(role))
                        toast.info(`${u.name} already holds ${roleName(role)}`, {
                          description: "Change their roles in the list.",
                        });
                      else add.mutate({ userId: u.id, roles: [...(m?.direct_roles ?? []), role] });
                    }}
                  />
                </div>
                <Field label="As" htmlFor="member-role" className="w-48">
                  <Select
                    id="member-role"
                    value={role}
                    onChange={setRole}
                    loading={offered.isPending}
                    options={(offered.data?.items ?? []).map((r) => ({
                      value: r.key,
                      label: r.name,
                      group: r.built_in ? "Built in" : "Custom",
                    }))}
                  />
                </Field>
              </div>
              <Refusal error={add.error} />
            </div>
          )
        }
        empty={
          <EmptyState
            icon={UserRound}
            title="Nobody yet"
            body={
              canManage
                ? "Add someone above, or map a group of the identity provider to a role in this project."
                : "Nobody holds a role in this project."
            }
          />
        }
        rowMenuLabel={(m) => `Actions for ${m.user.name}`}
        rowMenu={
          canManage && offered.data
            ? (m) => (
                <>
                  <MenuItem onSelect={() => setEditing(m)}>Change roles</MenuItem>
                  {removable(m) && (
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
      {editing && offered.data && (
        <RolesSheet project={project} member={editing} offered={offered.data.items} onClose={() => setEditing(null)} />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.user.name}?`}
          description={`They lose the roles given to them in ${project.name}: ${removing.direct_roles.map(roleName).join(", ")}.`}
          consequences={
            kept(removing).length > 0
              ? `They keep ${kept(removing)
                  .map((r) => `${r.name} (${sourceText({ ...r, direct: false })})`)
                  .join(", ")}: a group gives it.`
              : "Their deployments stay in the project."
          }
          confirmLabel="Remove member"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Panel>
  );
}

function RuleSheet({
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
        className={mono ? "font-mono text-secondary" : undefined}
        value={f[k]}
        onChange={(e) => setF({ ...f, [k]: e.target.value })}
      />
    </Field>
  );
  const segField = (k: keyof typeof seg, label: string, hint?: string) => (
    <Field label={label} htmlFor={`rule-seg-${k}`} error={errorAt(errors, `/segments/${k}`)} hint={hint}>
      <Input
        id={`rule-seg-${k}`}
        className="font-mono text-secondary"
        value={seg[k]}
        onChange={(e) => setSeg({ ...seg, [k]: e.target.value })}
      />
    </Field>
  );
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${rule ? "Change" : "New"} placement rule for ${project.name} in ${env}`}
      description="Names take * patterns and are comma-separated; tags are category/tag, all of them needed."
      submitLabel={save.isPending ? "Saving…" : "Save rule"}
      submitDisabled={save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
      className="w-[min(640px,100vw)]"
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
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
      <label htmlFor="rule-seg-on" className="flex items-center gap-2 text-body">
        <Checkbox id="rule-seg-on" checked={segOn} onChange={setSegOn} />
        NSX segments made for deployments
      </label>
      {segOn && (
        <fieldset className="grid grid-cols-1 gap-3 rounded-md border border-line p-3 sm:grid-cols-2">
          <legend className="px-1 text-secondary text-muted">
            Items that make their own networks get segments on this Tier-1, their subnets from this block
          </legend>
          {segField("tier1", "Tier-1 gateway", "Its NSX ID, for example t1-gva-dev")}
          {segField("ip_block", "EfficientIP block", "Its name or range")}
          {segField("prefix_length", "Subnet size", "Prefix length, 16 to 29")}
          {segField("dns_zone", "DNS zone")}
          {segField("dns_servers", "DNS servers", "Comma-separated")}
        </fieldset>
      )}
    </Sheet>
  );
}

const names = (plain: string[], tags: string[]) => [...plain, ...tags.map((t) => `tag ${t}`)].join(", ") || "Any";

function Placement({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const qc = useQueryClient();
  const [env, setEnv] = useState(project.environments[0] ?? "dev");
  const [editing, setEditing] = useState<S["PlacementRule"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["PlacementRule"] | null>(null);
  const rules = useQuery(adm.placementRules(project.id));
  const preview = useQuery(adm.placementPreview(project.id, env));
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/placement-rules/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "placement-rules"] });
      void qc.invalidateQueries({ queryKey: ["admin", "placement-preview"] });
      toast.success("Placement rule removed");
      setRemoving(null);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const here = rules.data?.items.filter((r) => r.environment === env);
  const columns: Column<S["PlacementRule"]>[] = [
    {
      id: "priority",
      header: "Priority",
      sortValue: (r) => r.priority,
      cell: (r) => <span className="tabular-nums">{r.priority}</span>,
      className: "w-20",
    },
    {
      id: "clusters",
      header: "Clusters",
      cell: (r) => (
        <div className="flex flex-col font-mono text-secondary">
          <span>{names(r.clusters, r.cluster_tags)}</span>
          {r.vcenter && <span className="text-muted">in {r.vcenter}</span>}
        </div>
      ),
    },
    {
      id: "datastores",
      header: "Datastores",
      cell: (r) => (
        <div className="flex flex-col text-secondary">
          <span className="font-mono">{names(r.datastores, r.datastore_tags)}</span>
          {r.storage_policy && <span className="text-muted">{r.storage_policy}</span>}
        </div>
      ),
    },
    {
      id: "networks",
      header: "Networks",
      cell: (r) => (
        <div className="flex flex-col font-mono text-secondary">
          <span>{r.networks.join(", ") || "None"}</span>
          {r.segments && (
            <span className="text-muted">
              NSX {r.segments.tier1}, {r.segments.ip_block} /{r.segments.prefix_length}, {r.segments.dns_zone}
            </span>
          )}
        </div>
      ),
    },
    {
      id: "folder",
      header: "VM folder",
      cell: (r) => <span className="font-mono text-secondary break-all">{r.folder}</span>,
    },
  ];
  const add = isAdmin && (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New rule
    </Button>
  );
  return (
    <div className="flex flex-col gap-4">
      <Field label="Environment" htmlFor="placement-env" className="w-48">
        <Select
          id="placement-env"
          value={env}
          onChange={setEnv}
          options={project.environments.map((e) => ({ value: e, label: e }))}
        />
      </Field>
      <Panel
        title="Placement rules"
        description={`Where requests of ${project.name} in ${env} go: the first rule that fits, lowest priority first.`}
        flush
        actions={here?.length ? add : undefined}
      >
        <DataTable
          label={`Placement rules in ${env}`}
          columns={columns}
          rows={here}
          rowKey={(r) => r.id}
          minWidth={720}
          defaultSort={{ id: "priority", desc: false }}
          loading={rules.isPending}
          error={rules.isError && <ErrorState error={rules.error} onRetry={() => void rules.refetch()} />}
          empty={
            <EmptyState
              icon={RouteIcon}
              title="No rule"
              body={`Requests of ${project.name} in ${env} have nowhere to go.`}
              action={add || undefined}
            />
          }
          rowMenuLabel={(r) => `Actions for the rule of priority ${r.priority}`}
          rowMenu={
            isAdmin
              ? (r) => (
                  <>
                    <MenuItem onSelect={() => setEditing(r)}>Change</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(r)}>
                      Remove
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      <Panel title={`What ${project.name} gets in ${env}`}>
        {preview.isPending && <Skeleton className="h-24" />}
        {preview.data && (
          <div className="flex flex-col gap-2 text-secondary">
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
        <RuleSheet
          project={project}
          env={env}
          rule={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove the rule of priority ${removing.priority} in ${env}?`}
          description={`Requests of ${project.name} in ${env} are placed by the rules left.`}
          consequences={
            (here?.length ?? 0) <= 1
              ? `It is the only rule: requests of ${project.name} in ${env} will have nowhere to go.`
              : `${(here?.length ?? 1) - 1} other rules stay. Deployments already placed stay where they are.`
          }
          confirmLabel="Remove rule"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
        />
      )}
    </div>
  );
}

const resources: { value: S["QuotaResource"]; label: string }[] = [
  { value: "vcpu", label: "vCPU" },
  { value: "memory_gb", label: "Memory (GB)" },
  { value: "storage_gb", label: "Storage (GB)" },
  { value: "vm_count", label: "VMs" },
];
const resourceName = (r: S["QuotaResource"]) => resources.find((x) => x.value === r)?.label ?? r;
const enforcements = [
  { value: "hard", label: "Refused" },
  { value: "soft", label: "Needs approval" },
];

function useSaveQuotas(project: S["AdminProject"]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (items: S["QuotaLimit"][]) =>
      call(
        api.PUT("/admin/projects/{projectId}/quotas", {
          params: { path: { projectId: project.id } },
          body: { items },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "quotas", project.id] });
      void qc.invalidateQueries({ queryKey: ["projects", project.id, "quota"] });
    },
    onError: (e) => failed(e, "Not saved"),
  });
}

/** Saving replaces every limit: the sheet saves the list with this one added or changed. */
function LimitSheet({
  project,
  limits,
  index,
  onClose,
}: {
  project: S["AdminProject"];
  limits: S["QuotaLimit"][];
  /** The limit changed; none for a new one. */
  index?: number;
  onClose: () => void;
}) {
  const save = useSaveQuotas(project);
  const existing = index === undefined ? undefined : limits[index];
  const [f, setF] = useState({
    resource: existing?.resource ?? "vcpu",
    environment: existing?.environment ?? "",
    limit: existing?.limit == null ? "" : String(existing.limit),
    enforcement: existing?.enforcement ?? "hard",
  });
  const twin = limits.some((l, i) => i !== index && l.resource === f.resource && l.environment === f.environment);
  const value = Number(f.limit);
  const valid = f.limit.trim() === "" || (Number.isInteger(value) && value >= 0);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={existing ? "Change the limit" : "New limit"}
      description="Requests that would pass a limit are refused, or need an approval."
      submitLabel={save.isPending ? "Saving…" : "Save limit"}
      submitDisabled={twin || !valid || save.isPending}
      onSubmit={() => {
        const row: S["QuotaLimit"] = {
          resource: f.resource,
          environment: f.environment,
          limit: f.limit.trim() === "" ? null : value,
          enforcement: f.enforcement,
        };
        const items = existing ? limits.map((l, i) => (i === index ? row : l)) : [...limits, row];
        save.mutate(items, {
          onSuccess: () => {
            toast.success("Limit saved");
            onClose();
          },
        });
      }}
    >
      <Field label="Resource" htmlFor="limit-resource">
        <Select
          id="limit-resource"
          value={f.resource}
          onChange={(v) => setF({ ...f, resource: v as S["QuotaResource"] })}
          options={resources}
        />
      </Field>
      <Field
        label="In"
        htmlFor="limit-environment"
        error={twin ? "This resource has a limit there already: change that one." : undefined}
      >
        <Select
          id="limit-environment"
          value={f.environment || "all"}
          onChange={(v) => setF({ ...f, environment: v === "all" ? "" : v })}
          options={[
            { value: "all", label: "The whole project" },
            ...project.environments.map((e) => ({ value: e, label: e })),
          ]}
        />
      </Field>
      <Field
        label="Limit"
        htmlFor="limit-value"
        hint="Empty for no limit."
        error={valid ? undefined : "A whole number, 0 or more."}
      >
        <Input
          id="limit-value"
          type="number"
          min={0}
          className="w-32"
          value={f.limit}
          onChange={(e) => setF({ ...f, limit: e.target.value })}
        />
      </Field>
      <Field label="When passed" htmlFor="limit-enforcement">
        <Select
          id="limit-enforcement"
          value={f.enforcement}
          onChange={(v) => setF({ ...f, enforcement: v as S["QuotaLimit"]["enforcement"] })}
          options={enforcements}
        />
      </Field>
    </Sheet>
  );
}

/** What a limit's resource takes now, in its environment or the whole project. */
function LimitUse({ project, limit }: { project: S["AdminProject"]; limit: S["QuotaLimit"] }) {
  const usage = useQuery(q.quota(project.id, limit.environment || undefined));
  if (usage.isPending) return <Skeleton className="h-4 w-20" />;
  const line = usage.data?.lines.find((l) => l.resource === limit.resource);
  if (!line) return <span className="text-faint">Not known</span>;
  const shown = { ...line, limit: limit.limit };
  return <span className={cn("tabular-nums", quotaTone(shown))}>{takenText(shown)}</span>;
}

type Row = S["QuotaLimit"] & { index: number };

function Quotas({ project, isAdmin }: { project: S["AdminProject"]; isAdmin: boolean }) {
  const quotas = useQuery(adm.quotas(project.id));
  const save = useSaveQuotas(project);
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const [removing, setRemoving] = useState<Row | null>(null);
  const limits = quotas.data?.items ?? [];
  const where = (l: S["QuotaLimit"]) => (l.environment ? `in ${l.environment}` : "in the whole project");
  const columns: Column<Row>[] = [
    {
      id: "resource",
      header: "Resource",
      sortValue: (l) => resourceName(l.resource),
      cell: (l) => <span className="font-medium">{resourceName(l.resource)}</span>,
    },
    {
      id: "in",
      header: "In",
      sortValue: (l) => l.environment,
      cell: (l) => <span className="text-secondary">{l.environment || "The whole project"}</span>,
    },
    {
      id: "limit",
      header: "Limit",
      align: "end",
      sortValue: (l) => l.limit ?? -1,
      cell: (l) => <span className="tabular-nums">{l.limit ?? "None"}</span>,
    },
    {
      id: "used",
      header: "Taken now",
      align: "end",
      cell: (l) => <LimitUse project={project} limit={l} />,
    },
    {
      id: "enforcement",
      header: "When passed",
      cell: (l) => <span className="text-secondary">{enforcements.find((e) => e.value === l.enforcement)?.label}</span>,
    },
  ];
  const add = isAdmin && (
    <Button size="sm" variant="primary" disabled={!quotas.data} onClick={() => setEditing("new")}>
      Add a limit
    </Button>
  );
  return (
    <Panel
      title="Quotas"
      description="What the project's requests may take, with what its deployments and running requests take now."
      flush
      actions={limits.length ? add : undefined}
    >
      <DataTable
        label="Quota limits"
        columns={columns}
        rows={quotas.data && limits.map((l, index) => ({ ...l, index }))}
        rowKey={(l) => `${l.resource}/${l.environment}/${l.index}`}
        minWidth={640}
        loading={quotas.isPending}
        error={quotas.isError && <ErrorState error={quotas.error} onRetry={() => void quotas.refetch()} />}
        empty={
          <EmptyState
            icon={Gauge}
            title="No limit"
            body="The project's requests may take any amount. Add a limit on vCPU, memory, storage or VMs."
            action={add || undefined}
          />
        }
        rowMenuLabel={(l) => `Actions for the ${resourceName(l.resource)} limit ${where(l)}`}
        rowMenu={
          isAdmin
            ? (l) => (
                <>
                  <MenuItem onSelect={() => setEditing(l.index)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(l)}>
                    Remove
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing !== null && (
        <LimitSheet
          project={project}
          limits={limits}
          index={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove the ${resourceName(removing.resource)} limit ${where(removing)}?`}
          description="Requests are no longer held to it."
          consequences={
            removing.environment
              ? "A limit on the whole project still applies, if there is one."
              : "Limits in each environment still apply."
          }
          confirmLabel="Remove limit"
          pending={save.isPending}
          onConfirm={() =>
            save.mutate(
              limits.filter((_, i) => i !== removing.index),
              {
                onSuccess: () => {
                  toast.success("Limit removed");
                  setRemoving(null);
                },
              },
            )
          }
        />
      )}
    </Panel>
  );
}

export function ProjectPage({ projectId, tab = "members" }: { projectId: string; tab?: ProjectTab }) {
  const navigate = useNavigate();
  const { me } = useCurrentProject();
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
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/projects">Projects</Link>
          </Breadcrumbs>
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
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({
            to: "/admin/projects/$projectId",
            params: { projectId },
            search: { tab: next as ProjectTab },
            replace: true,
          })
        }
      >
        <TabsList aria-label="Project sections">
          <TabsTrigger value="members">Members</TabsTrigger>
          <TabsTrigger value="placement">Placement</TabsTrigger>
          <TabsTrigger value="quotas">Quotas</TabsTrigger>
          <TabsTrigger value="details">Details</TabsTrigger>
        </TabsList>
        <TabsContent value="members" className="pt-4">
          <Members project={p} canManage={can(me, "project.members.manage", p.id)} />
        </TabsContent>
        <TabsContent value="placement" className="pt-4">
          <Placement project={p} isAdmin={can(me, "placement.manage", p.id)} />
        </TabsContent>
        <TabsContent value="quotas" className="pt-4">
          <Quotas project={p} isAdmin={can(me, "project.quotas.manage", p.id)} />
        </TabsContent>
        <TabsContent value="details" className="pt-4">
          <Details
            key={[p.name, p.description, p.cost_center, p.environments.join(), p.archived].join("|")}
            project={p}
            isAdmin={can(me, "project.update", p.id)}
          />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
