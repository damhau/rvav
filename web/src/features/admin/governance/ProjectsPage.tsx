// Projects, environments and identity provider groups (S9, ADR-0020).
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { FolderKanban, Layers, UsersRound } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, TableSearch, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { UserRef } from "@/lib/api/types";
import { adm, q } from "@/lib/query";
import { can, projectsWith } from "@/lib/can";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { cn } from "@/lib/utils";
import { AdminWarning, Refusal, refused } from "../access/refusal";
import { failed, fieldErrors, UserPicker } from "./common";
import { DirectoryPlacements } from "./DirectoryPlacements";

type S = components["schemas"];

export const projectsTabs = ["projects", "environments", "groups"] as const;
export type ProjectsTab = (typeof projectsTabs)[number];

/** A project, a member, a mapping: what changes with it is everywhere, /me too. */
function useRefreshAccess() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["admin"] });
    void qc.invalidateQueries({ queryKey: ["me"] });
  };
}

function NewProjectSheet({ onClose }: { onClose: () => void }) {
  const refresh = useRefreshAccess();
  const navigate = useNavigate();
  const envs = useQuery(q.environments());
  const [f, setF] = useState({
    code: "",
    name: "",
    description: "",
    cost_center: "",
    environments: ["dev"] as string[],
  });
  const [admins, setAdmins] = useState<UserRef[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => call(api.POST("/admin/projects", { body: { ...f, project_admins: admins.map((a) => a.id) } })),
    onSuccess: (p) => {
      refresh();
      toast.success(`${p.name} created`);
      void navigate({ to: "/admin/projects/$projectId", params: { projectId: p.id } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The project wasn't created");
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={create.isPending}
      title="New project"
      description="Its code is in VM names and can't change later."
      submitLabel={create.isPending ? "Creating…" : "Create"}
      submitDisabled={!f.code || !f.name}
      onSubmit={() => {
        setErrors({});
        create.mutate();
      }}
    >
      <div className="grid grid-cols-[120px_1fr] gap-3">
        <Field label="Code" htmlFor="project-code" error={errors["/code"]}>
          <Input
            id="project-code"
            className="font-mono"
            value={f.code}
            onChange={(e) => setF({ ...f, code: e.target.value })}
          />
        </Field>
        <Field label="Name" htmlFor="project-name" error={errors["/name"]}>
          <Input id="project-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
      </div>
      <Field label="Description" htmlFor="project-description">
        <Input
          id="project-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Field label="Cost centre" htmlFor="project-cost">
        <Input id="project-cost" value={f.cost_center} onChange={(e) => setF({ ...f, cost_center: e.target.value })} />
      </Field>
      <fieldset className="flex flex-wrap gap-3">
        <legend className="mb-1 text-secondary font-medium">Environments it deploys to</legend>
        {(envs.data?.items ?? []).map((e) => (
          <label key={e.key} htmlFor={`project-env-${e.key}`} className="flex items-center gap-1.5 text-body">
            <Checkbox
              id={`project-env-${e.key}`}
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
      <UserPicker
        label="Project admins"
        id="project-admin-search"
        onPick={(u) => !admins.some((a) => a.id === u.id) && setAdmins([...admins, u])}
      />
      {admins.length > 0 && (
        <ul aria-label="Chosen project admins" className="flex flex-wrap gap-1.5">
          {admins.map((a) => (
            <li key={a.id}>
              <Badge tone="accent">
                {a.name}{" "}
                <button
                  type="button"
                  aria-label={`Remove ${a.name}`}
                  onClick={() => setAdmins(admins.filter((x) => x.id !== a.id))}
                >
                  ×
                </button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** Archiving asks, and so does putting back in use: either changes what its members may do. */
export function ArchiveDialog({ project, onClose }: { project: S["AdminProject"]; onClose: () => void }) {
  const refresh = useRefreshAccess();
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PATCH("/admin/projects/{projectId}", {
          params: { path: { projectId: project.id } },
          body: { archived: !project.archived },
        }),
      ),
    onSuccess: (p) => {
      refresh();
      toast.success(p.archived ? `${p.name} archived` : `${p.name} back in use`);
      onClose();
    },
    onError: (e) => failed(e, "Not changed"),
  });
  const common = { open: true, onOpenChange: (o: boolean) => !o && onClose(), pending: save.isPending };
  if (project.archived)
    return (
      <ConfirmDialog
        {...common}
        tone="primary"
        title={`Put ${project.name} back in use?`}
        description="Its members can request in it again, with its placement rules and quotas as they were."
        confirmLabel="Put back in use"
        onConfirm={() => save.mutate()}
      />
    );
  return (
    <ConfirmDialog
      {...common}
      title={`Archive ${project.name}?`}
      description="Nobody can request anything new in it, and it leaves the project lists. You can put it back in use later."
      consequences={
        project.deployments === 0
          ? "It has no deployment."
          : project.deployments === 1
            ? "Its deployment goes on running, and its members keep it."
            : `Its ${project.deployments} deployments go on running, and its members keep them.`
      }
      confirmLabel="Archive project"
      onConfirm={() => save.mutate()}
    />
  );
}

const resourceLabel: Record<S["QuotaResource"], [string, string?]> = {
  vcpu: ["vCPU"],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs"],
};

type QuotaLine = S["QuotaUsage"]["lines"][number];

/** What deployments and running requests take, of the limit: "12 of 64 GB". */
export function takenText(l: QuotaLine): string {
  const unit = resourceLabel[l.resource][1];
  const taken = l.used + l.reserved;
  return `${l.limit == null ? taken : `${taken} of ${l.limit}`}${unit ? ` ${unit}` : ""}`;
}

export function quotaTone(l: QuotaLine): string | undefined {
  if (l.limit == null) return undefined;
  const taken = l.used + l.reserved;
  if (taken >= l.limit) return "text-bad";
  if (taken >= 0.8 * l.limit) return "text-warn";
  return undefined;
}

/** The project-wide limit nearest to being reached. */
function QuotaUse({ projectId }: { projectId: string }) {
  const quota = useQuery(q.quota(projectId));
  if (quota.isPending) return <Skeleton className="h-4 w-24" />;
  if (quota.isError) return <span className="text-faint">Not known</span>;
  const tightest = quota.data.lines
    .filter((l) => l.limit != null && l.limit > 0)
    .sort((a, b) => (b.used + b.reserved) / b.limit! - (a.used + a.reserved) / a.limit!)[0];
  if (!tightest) return <span className="text-muted">No project-wide limit</span>;
  return (
    <span className={cn("tabular-nums", quotaTone(tightest))}>
      {resourceLabel[tightest.resource][0]} {takenText(tightest)}
    </span>
  );
}

function ProjectsPanel() {
  const navigate = useNavigate();
  const [search, setSearch] = useState<string>();
  const [text, setText] = useSearchBox(search, setSearch);
  const [archived, setArchived] = useState(false);
  const [creating, setCreating] = useState(false);
  const [archiving, setArchiving] = useState<S["AdminProject"] | null>(null);
  const { me } = useCurrentProject();
  const scope = projectsWith(me, "project.settings.read");
  const projects = useQuery({
    ...adm.projects(search ?? "", archived),
    enabled: scope === "all",
    placeholderData: keepPreviousData,
  });
  if (scope !== "all") {
    // The projects whose settings the caller sees: a project admin's own.
    return (
      <Panel title="Your projects">
        <ul className="flex flex-col gap-1 text-body">
          {scope.map((p) => (
            <li key={p.id}>
              <Link
                to="/admin/projects/$projectId"
                params={{ projectId: p.id }}
                className="font-medium hover:text-accent"
              >
                {p.name}
              </Link>
            </li>
          ))}
        </ul>
      </Panel>
    );
  }
  const columns: Column<S["AdminProject"]>[] = [
    {
      id: "project",
      header: "Project",
      sortValue: (p) => p.name.toLowerCase(),
      cell: (p) => (
        <div className="flex flex-wrap items-center gap-x-2">
          <Link to="/admin/projects/$projectId" params={{ projectId: p.id }} className="font-medium hover:text-accent">
            {p.name}
          </Link>
          <span className="font-mono text-caption text-faint">{p.code}</span>
          {p.archived && <Badge tone="neutral">Archived</Badge>}
        </div>
      ),
    },
    {
      id: "environments",
      header: "Environments",
      cell: (p) => <span className="text-secondary text-muted">{p.environments.join(", ") || "None"}</span>,
    },
    {
      id: "members",
      header: "Members",
      align: "end",
      sortValue: (p) => p.members,
      cell: (p) => <span className="tabular-nums">{p.members}</span>,
    },
    {
      id: "deployments",
      header: "Deployments",
      align: "end",
      sortValue: (p) => p.deployments,
      cell: (p) => <span className="tabular-nums">{p.deployments}</span>,
    },
    {
      id: "quota",
      header: "Quota use",
      cell: (p) => (
        <span className="text-secondary">
          <QuotaUse projectId={p.id} />
        </span>
      ),
    },
  ];
  const create = can(me, "project.create") && (
    <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
      New project
    </Button>
  );
  const filtered = Boolean(search) || archived;
  // With no project at all, the empty state offers New project itself.
  const none = projects.data?.items.length === 0 && !filtered;
  return (
    <Panel title="Projects" flush actions={none ? undefined : create}>
      <DataTable
        label="Projects"
        columns={columns}
        rows={projects.data?.items}
        rowKey={(p) => p.id}
        rowLinks
        minWidth={720}
        loading={projects.isPending}
        error={projects.isError && <ErrorState error={projects.error} onRetry={() => void projects.refetch()} />}
        toolbar={
          <>
            <TableSearch label="Search projects" value={text} onChange={setText} placeholder="Name or code" />
            <label htmlFor="projects-archived" className="flex items-center gap-1.5 text-secondary">
              <Checkbox id="projects-archived" checked={archived} onChange={setArchived} />
              Archived ones too
            </label>
          </>
        }
        empty={
          filtered ? (
            <EmptyState
              icon={FolderKanban}
              title="No project matches"
              body="Search by name or code."
              action={
                <Button
                  size="sm"
                  onClick={() => {
                    setSearch(undefined);
                    setArchived(false);
                  }}
                >
                  Clear the search
                </Button>
              }
            />
          ) : (
            <EmptyState
              icon={FolderKanban}
              title="No projects yet"
              body="A project is who requests: its members, the environments it deploys to, where its VMs go and its quotas."
              action={create || undefined}
            />
          )
        }
        rowMenuLabel={(p) => `Actions for ${p.name}`}
        rowMenu={(p) => (
          <>
            <MenuItem onSelect={() => void navigate({ to: "/admin/projects/$projectId", params: { projectId: p.id } })}>
              Open
            </MenuItem>
            {can(me, "project.update", p.id) && (
              <>
                <MenuSeparator />
                <MenuItem danger={!p.archived} onSelect={() => setArchiving(p)}>
                  {p.archived ? "Put back in use" : "Archive"}
                </MenuItem>
              </>
            )}
          </>
        )}
      />
      {creating && <NewProjectSheet onClose={() => setCreating(false)} />}
      {archiving && <ArchiveDialog project={archiving} onClose={() => setArchiving(null)} />}
    </Panel>
  );
}

function useRefreshEnvironments() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["admin", "environments"] });
    void qc.invalidateQueries({ queryKey: ["environments"] });
  };
}

function EnvironmentSheet({
  existing,
  next,
  onClose,
}: {
  existing?: S["AdminEnvironment"];
  /** The order a new environment gets: after the last one. */
  next: number;
  onClose: () => void;
}) {
  const refresh = useRefreshEnvironments();
  const [f, setF] = useState({
    key: existing?.key ?? "",
    name: existing?.name ?? "",
    production: existing?.production ?? false,
    snapshots: String(existing?.snapshot_max_count ?? 3),
    tagCategories: existing?.tag_categories ?? [],
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const snapshots = Number(f.snapshots);
  const tag_categories = f.tagCategories;
  const save = useMutation({
    mutationFn: () =>
      existing
        ? call(
            api.PATCH("/admin/environments/{key}", {
              params: { path: { key: existing.key } },
              body: { name: f.name, production: f.production, snapshot_max_count: snapshots, tag_categories },
            }),
          )
        : call(
            api.POST("/admin/environments", {
              body: {
                key: f.key,
                name: f.name,
                production: f.production,
                order: next,
                snapshot_max_count: snapshots,
                tag_categories,
              },
            }),
          ),
    onSuccess: () => {
      refresh();
      toast.success(existing ? `${f.name} saved` : `${f.name} added`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, existing ? "Not saved" : "Not added");
    },
  });
  const whole = f.snapshots.trim() !== "" && Number.isInteger(snapshots) && snapshots >= 0;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? `Change ${existing.name}` : "New environment"}
      description="Projects deploy to environments; a VM's name carries its environment's key."
      submitLabel={save.isPending ? "Saving…" : existing ? "Save environment" : "Add environment"}
      submitDisabled={!f.key || !f.name || !whole}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field
        label="Key"
        htmlFor="env-key"
        hint="2 to 10 lower-case letters and digits. It can't change later."
        error={errors["/key"]}
      >
        <Input
          id="env-key"
          className="font-mono"
          placeholder="dev"
          value={f.key}
          disabled={Boolean(existing)}
          onChange={(e) => setF({ ...f, key: e.target.value })}
        />
      </Field>
      <Field label="Name" htmlFor="env-name" error={errors["/name"]}>
        <Input
          id="env-name"
          placeholder="Development"
          value={f.name}
          onChange={(e) => setF({ ...f, name: e.target.value })}
        />
      </Field>
      <label htmlFor="env-production" className="flex items-start gap-2 text-body">
        <Checkbox
          id="env-production"
          checked={f.production}
          onChange={(on) => setF({ ...f, production: on })}
          className="mt-0.5"
        />
        <span>
          Production
          <span className="block text-secondary text-muted">
            Highlighted everywhere; policies are usually stricter.
          </span>
        </span>
      </label>
      <Field label="Snapshots kept" htmlFor="env-snapshots" hint="Per VM." error={errors["/snapshot_max_count"]}>
        <Input
          id="env-snapshots"
          type="number"
          min={0}
          className="w-24"
          value={f.snapshots}
          onChange={(e) => setF({ ...f, snapshots: e.target.value })}
        />
      </Field>
      <TagCategoriesField
        value={f.tagCategories}
        onChange={(tagCategories) => setF({ ...f, tagCategories })}
        error={errors["/tag_categories"]}
      />
    </Sheet>
  );
}

/**
 * The vSphere tag categories an environment's VMs' users may change with
 * Edit tags (X1.3): those vCenter has for VMs, and any already chosen.
 */
function TagCategoriesField({
  value,
  onChange,
  error,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  error?: string;
}) {
  const inventory = useQuery(adm.inventory());
  const known = new Map<string, S["InventoryTagCategory"]>();
  for (const c of inventory.data?.tag_categories ?? []) if (!known.has(c.name)) known.set(c.name, c);
  const names = [...new Set([...known.keys(), ...value])].sort((a, b) => a.localeCompare(b));
  const toggle = (name: string, on: boolean) =>
    onChange(on ? [...value, name].sort((a, b) => a.localeCompare(b)) : value.filter((v) => v !== name));
  return (
    <fieldset className="flex flex-col gap-1.5" aria-describedby="env-tag-categories-note">
      <legend className="mb-1 text-secondary font-medium">Tags users may change</legend>
      {inventory.isPending ? (
        <Skeleton className="h-5 w-40" />
      ) : names.length === 0 ? (
        <p className="text-secondary text-muted">
          {inventory.isError
            ? "Couldn't read vCenter's tag categories."
            : "vCenter has no tag category for VMs yet: rvav reads them at its inventory sync."}
        </p>
      ) : (
        names.map((name) => {
          const c = known.get(name);
          const id = `env-tag-category-${name.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
          return (
            <div key={name} className="flex items-baseline gap-2 text-body">
              <Checkbox id={id} checked={value.includes(name)} onChange={(on) => toggle(name, on)} />
              <label htmlFor={id} className="font-mono text-secondary">
                {name}
              </label>
              <span className="min-w-0 truncate text-caption text-faint">
                {c
                  ? [c.single && "one per VM", c.tags.join(", ") || "no tags yet"].filter(Boolean).join(" · ")
                  : "not in vCenter"}
              </span>
            </div>
          );
        })
      )}
      <FieldNote
        id="env-tag-categories-note"
        error={error}
        hint="Their tags are what Edit tags offers on this environment's VMs. Tags of other categories stay as they are."
      />
    </fieldset>
  );
}

function EnvironmentsPanel({ canEdit }: { canEdit: boolean }) {
  const refresh = useRefreshEnvironments();
  const envs = useQuery(adm.environments());
  const [editing, setEditing] = useState<S["AdminEnvironment"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["AdminEnvironment"] | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/environments/{key}", { params: { path: { key } } })),
    onSuccess: () => {
      refresh();
      toast.success(`${removing?.name ?? "The environment"} removed`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const list = envs.data?.items;
  const columns: Column<S["AdminEnvironment"]>[] = [
    {
      id: "environment",
      header: "Environment",
      sortValue: (e) => e.order,
      cell: (e) => (
        <div className="flex flex-wrap items-center gap-x-2">
          <span className="font-medium">{e.name}</span>
          <span className="font-mono text-caption text-faint">{e.key}</span>
        </div>
      ),
    },
    {
      id: "production",
      header: "Production",
      cell: (e) => (e.production ? <Badge tone="warn">Production</Badge> : <span className="text-muted">No</span>),
    },
    {
      id: "snapshots",
      header: "Snapshots kept",
      align: "end",
      sortValue: (e) => e.snapshot_max_count,
      cell: (e) => <span className="tabular-nums">{e.snapshot_max_count}</span>,
    },
    {
      id: "tags",
      header: "Tags users change",
      cell: (e) =>
        e.tag_categories?.length ? (
          <span className="font-mono text-secondary">{e.tag_categories.join(", ")}</span>
        ) : (
          <span className="text-muted">None</span>
        ),
    },
    {
      id: "used",
      header: "Used by",
      cell: (e) => (
        <span className="text-secondary text-muted">
          {plural(e.projects ?? 0, "project")}, {plural(e.deployments ?? 0, "deployment")}
        </span>
      ),
    },
  ];
  const add = canEdit && (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New environment
    </Button>
  );
  const why = (e: S["AdminEnvironment"]) =>
    (e.projects ?? 0) > 0
      ? "Remove: projects deploy to it"
      : (e.deployments ?? 0) > 0
        ? "Remove: deployments are in it"
        : undefined;
  return (
    <Panel
      title="Environments"
      description="Where projects deploy, in this order everywhere."
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label="Environments"
        columns={columns}
        rows={list}
        rowKey={(e) => e.key}
        minWidth={680}
        loading={envs.isPending}
        error={envs.isError && <ErrorState error={envs.error} onRetry={() => void envs.refetch()} />}
        empty={
          <EmptyState
            icon={Layers}
            title="No environments yet"
            body="Each project deploys to environments: add one, development for example, before the first project."
            action={add || undefined}
          />
        }
        rowMenuLabel={(e) => `Actions for ${e.name}`}
        rowMenu={
          canEdit
            ? (e) => (
                <>
                  <MenuItem onSelect={() => setEditing(e)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger disabled={Boolean(why(e))} onSelect={() => setRemoving(e)}>
                    {why(e) ?? "Remove"}
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && (
        <EnvironmentSheet
          existing={editing === "new" ? undefined : editing}
          next={Math.max(0, ...(list ?? []).map((e) => e.order)) + 1}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description="Projects can't be given it any more."
          consequences="No project deploys to it and no deployment is in it."
          confirmLabel="Remove environment"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.key)}
        />
      )}
    </Panel>
  );
}

function MapGroupSheet({ onClose }: { onClose: () => void }) {
  const refresh = useRefreshAccess();
  const projects = useQuery(adm.projectNames());
  const roles = useQuery(adm.roles());
  const [f, setF] = useState({ group: "", project: "none", role: "auditor" });
  const add = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/group-mappings", {
          body: { oidc_group: f.group, role: f.role, ...(f.project !== "none" ? { project_id: f.project } : {}) },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success(`${f.group} mapped`);
      onClose();
    },
    onError: (e) => {
      if (!refused(e)) failed(e, "Not mapped");
    },
  });
  // A role is mapped where it can be given: for all of rvav without a project, in one with it.
  const offered = (roles.data?.items ?? []).filter(
    (r) => r.key !== "user" && r.scope !== (f.project === "none" ? "project" : "rvav"),
  );
  const chosen = offered.find((r) => r.key === f.role);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={add.isPending}
      title="Map a group"
      description="Everyone in the identity provider's group gets the role, for all of rvav or in one project."
      submitLabel={add.isPending ? "Mapping…" : "Map group"}
      submitDisabled={!f.group}
      onSubmit={() => add.mutate()}
    >
      <Field label="OIDC group" htmlFor="map-group" hint="As the identity provider sends it in the groups claim.">
        <Input
          id="map-group"
          className="font-mono"
          placeholder="rvav-web"
          value={f.group}
          onChange={(e) => setF({ ...f, group: e.target.value })}
        />
      </Field>
      <Field label="In project" htmlFor="map-project">
        <Select
          id="map-project"
          value={f.project}
          onChange={(v) => setF({ ...f, project: v, role: v === "none" ? "auditor" : "member" })}
          options={[
            { value: "none", label: "For all of rvav" },
            ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
          ]}
        />
      </Field>
      <Field label="Role" htmlFor="map-role">
        <Select
          id="map-role"
          value={f.role}
          onChange={(v) => setF({ ...f, role: v })}
          loading={roles.isPending}
          options={offered.map((r) => ({ value: r.key, label: r.name, group: r.built_in ? "Built in" : "Custom" }))}
        />
      </Field>
      {chosen && !chosen.built_in && chosen.permissions.includes("access.manage") && (
        <AdminWarning who={`everyone in ${f.group || "the group"}`} />
      )}
      <Refusal error={add.error} />
    </Sheet>
  );
}

function GroupMappingsPanel({ canEdit }: { canEdit: boolean }) {
  const refresh = useRefreshAccess();
  const mappings = useQuery(adm.groupMappings());
  const [mapping, setMapping] = useState(false);
  const [removing, setRemoving] = useState<S["GroupMapping"] | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/group-mappings/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      refresh();
      toast.success(`The mapping of ${removing?.oidc_group ?? "the group"} removed`);
      setRemoving(null);
    },
    onError: (e) => {
      setRemoving(null);
      if (!refused(e)) failed(e, "Not removed");
    },
  });
  const gives = (m: S["GroupMapping"]) =>
    `${m.role_name ?? m.role}${m.project ? ` in ${m.project.name}` : " for all of rvav"}`;
  const columns: Column<S["GroupMapping"]>[] = [
    {
      id: "group",
      header: "Group",
      sortValue: (m) => m.oidc_group,
      cell: (m) => <span className="font-mono text-secondary">{m.oidc_group}</span>,
    },
    {
      id: "gives",
      header: "Gives",
      sortValue: (m) => gives(m),
      cell: (m) => (
        <span className="text-secondary">
          <Link
            to="/admin/roles/$roleKey"
            params={{ roleKey: m.role }}
            className="text-accent underline underline-offset-2"
          >
            {m.role_name ?? m.role}
          </Link>
          {m.project ? ` in ${m.project.name}` : " for all of rvav"}
        </span>
      ),
    },
    {
      id: "people",
      header: "People in it",
      align: "end",
      sortValue: (m) => m.users ?? 0,
      cell: (m) => <span className="tabular-nums">{m.users ?? 0}</span>,
    },
  ];
  const add = canEdit && (
    <Button size="sm" variant="primary" onClick={() => setMapping(true)}>
      Map a group
    </Button>
  );
  const list = mappings.data?.items;
  return (
    <Panel
      title="Identity provider groups"
      description="Roles given to everyone in a group of the identity provider."
      flush
      actions={list?.length ? add : undefined}
    >
      {refused(remove.error) && (
        <div className="border-b border-line px-4 py-3">
          <Refusal error={remove.error} />
        </div>
      )}
      <DataTable
        label="Identity provider groups"
        columns={columns}
        rows={list}
        rowKey={(m) => m.id}
        minWidth={560}
        loading={mappings.isPending}
        error={mappings.isError && <ErrorState error={mappings.error} onRetry={() => void mappings.refetch()} />}
        empty={
          <EmptyState
            icon={UsersRound}
            title="No group mapped yet"
            body="Map a group of the identity provider to admin first: the local administrator is then kept for when the provider is down."
            action={add || undefined}
          />
        }
        rowMenuLabel={(m) => `Actions for ${m.oidc_group}`}
        rowMenu={
          canEdit
            ? (m) => (
                <MenuItem danger onSelect={() => setRemoving(m)}>
                  Remove
                </MenuItem>
              )
            : undefined
        }
      />
      {mapping && <MapGroupSheet onClose={() => setMapping(false)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove the mapping of ${removing.oidc_group}?`}
          description={`People in ${removing.oidc_group} no longer get ${gives(removing)}, unless they hold it another way.`}
          consequences={
            (removing.users ?? 0) > 0
              ? `${plural(removing.users ?? 0, "person", "people")} ${removing.users === 1 ? "was" : "were"} in it at their last sign-in.`
              : "Nobody in it has signed in yet."
          }
          confirmLabel="Remove mapping"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
        />
      )}
    </Panel>
  );
}

export function ProjectsPage({ tab }: { tab?: ProjectsTab }) {
  const navigate = useNavigate();
  const { me } = useCurrentProject();
  const tabs = [
    { value: "projects", label: "Projects", show: true },
    { value: "environments", label: "Environments", show: can(me, "environments.read") },
    { value: "groups", label: "Identity provider groups", show: can(me, "access.read") },
  ].filter((t) => t.show);
  const shown = tabs.some((t) => t.value === tab) ? tab : "projects";
  return (
    <Page>
      <PageHeader
        title="Projects"
        description="Projects, the environments they deploy to, and the groups that give roles."
      />
      {tabs.length === 1 ? (
        <ProjectsPanel />
      ) : (
        <Tabs
          value={shown}
          onValueChange={(next) =>
            void navigate({ to: "/admin/projects", search: { tab: next as ProjectsTab }, replace: true })
          }
        >
          <TabsList aria-label="Projects sections">
            {tabs.map((t) => (
              <TabsTrigger key={t.value} value={t.value}>
                {t.label}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value="projects" className="pt-4">
            <ProjectsPanel />
          </TabsContent>
          {can(me, "environments.read") && (
            <TabsContent value="environments" className="flex flex-col gap-4 pt-4">
              <EnvironmentsPanel canEdit={can(me, "environments.manage")} />
              {can(me, "project.settings.read") && <DirectoryPlacements canManage={can(me, "placement.manage")} />}
            </TabsContent>
          )}
          {can(me, "access.read") && (
            <TabsContent value="groups" className="pt-4">
              <GroupMappingsPanel canEdit={can(me, "access.manage")} />
            </TabsContent>
          )}
        </Tabs>
      )}
    </Page>
  );
}
