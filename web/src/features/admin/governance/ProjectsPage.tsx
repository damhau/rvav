// Projects, environments and OIDC group mappings (S9, ADR-0020).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { UserRef } from "@/lib/api/types";
import { adm, q } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { failed, fieldErrors, td, th, UserPicker } from "./common";

function NewProject({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
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
      void qc.invalidateQueries({ queryKey: ["admin"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
      toast.success(`${p.name} created`);
      void navigate({ to: "/admin/projects/$projectId", params: { projectId: p.id } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "The project wasn't created");
    },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="New project" description="Its code is in VM names and can't change later.">
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
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
            <Input
              id="project-cost"
              value={f.cost_center}
              onChange={(e) => setF({ ...f, cost_center: e.target.value })}
            />
          </Field>
          <fieldset className="flex flex-wrap gap-3">
            <legend className="mb-1 text-xs font-medium">Environments it deploys to</legend>
            {(envs.data?.items ?? []).map((e) => (
              <label key={e.key} htmlFor={`project-env-${e.key}`} className="flex items-center gap-1.5 text-[13px]">
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
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!f.code || !f.name || create.isPending}>
              {create.isPending ? "Creating…" : "Create"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Projects() {
  const [search, setSearch] = useState("");
  const [archived, setArchived] = useState(false);
  const [creating, setCreating] = useState(false);
  const { me } = useCurrentProject();
  const isAdmin = me.global_roles.includes("admin");
  const seesAll = isAdmin || me.global_roles.includes("auditor");
  const projects = useQuery({ ...adm.projects(search, archived), enabled: seesAll });
  const list = projects.data?.items ?? [];
  if (!seesAll) {
    // A project admin's own projects.
    const mine = me.projects.filter((p) => p.role === "project-admin");
    return (
      <Panel title="Your projects">
        <ul className="flex flex-col gap-1 text-[13px]">
          {mine.map((p) => (
            <li key={p.project.id}>
              <Link
                to="/admin/projects/$projectId"
                params={{ projectId: p.project.id }}
                className="font-medium hover:text-accent"
              >
                {p.project.name}
              </Link>
            </li>
          ))}
        </ul>
      </Panel>
    );
  }
  return (
    <Panel
      title="Projects"
      flush
      actions={
        isAdmin && (
          <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
            New project
          </Button>
        )
      }
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2">
        <Input
          aria-label="Search projects"
          placeholder="Search"
          className="w-56"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <label htmlFor="projects-archived" className="flex items-center gap-1.5 text-xs">
          <Checkbox id="projects-archived" checked={archived} onChange={setArchived} />
          Archived ones too
        </label>
      </div>
      {projects.isPending && <SkeletonRows rows={3} cols={4} />}
      {projects.isError && <ErrorState error={projects.error} onRetry={() => void projects.refetch()} />}
      {projects.data && list.length === 0 && <EmptyState title="No project" body="Nothing matches." />}
      {list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Project
                </th>
                <th scope="col" className={th}>
                  Environments
                </th>
                <th scope="col" className={th}>
                  Members
                </th>
                <th scope="col" className={th}>
                  Deployments
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((p) => (
                <tr key={p.id} className="hover:bg-sunken">
                  <td className={`${td} pl-4`}>
                    <Link
                      to="/admin/projects/$projectId"
                      params={{ projectId: p.id }}
                      className="font-medium hover:text-accent"
                    >
                      {p.name}
                    </Link>{" "}
                    <span className="font-mono text-[11px] text-faint">{p.code}</span>
                    {p.archived && (
                      <Badge tone="neutral" className="ml-2">
                        Archived
                      </Badge>
                    )}
                  </td>
                  <td className={`${td} text-xs text-muted`}>{p.environments.join(", ")}</td>
                  <td className={`${td} text-xs text-muted`}>{p.members}</td>
                  <td className={`${td} text-xs text-muted`}>{p.deployments}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {creating && <NewProject onClose={() => setCreating(false)} />}
    </Panel>
  );
}

function Environments({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const envs = useQuery(adm.environments());
  const [f, setF] = useState({ key: "", name: "" });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "environments"] });
    void qc.invalidateQueries({ queryKey: ["environments"] });
  };
  const add = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/environments", {
          body: {
            key: f.key,
            name: f.name,
            production: false,
            order: (envs.data?.items.length ?? 0) + 1,
            snapshot_max_count: 3,
          },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success(`${f.name} added`);
      setF({ key: "", name: "" });
    },
    onError: (e) => failed(e, "Not added"),
  });
  const update = useMutation({
    mutationFn: ({ key, ...body }: { key: string; production?: boolean; snapshot_max_count?: number }) =>
      call(api.PATCH("/admin/environments/{key}", { params: { path: { key } }, body })),
    onSuccess: refresh,
    onError: (e) => failed(e, "Not saved"),
  });
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/environments/{key}", { params: { path: { key } } })),
    onSuccess: refresh,
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel title="Environments" flush>
      {envs.isPending && <SkeletonRows rows={3} cols={4} />}
      {envs.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Environment
                </th>
                <th scope="col" className={th}>
                  Production
                </th>
                <th scope="col" className={th}>
                  Snapshots kept
                </th>
                <th scope="col" className={th}>
                  Used by
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {envs.data.items.map((e) => (
                <tr key={e.key}>
                  <td className={`${td} pl-4`}>
                    {e.name} <span className="font-mono text-[11px] text-faint">{e.key}</span>
                  </td>
                  <td className={td}>
                    <Checkbox
                      id={`env-prod-${e.key}`}
                      disabled={!canEdit}
                      checked={e.production}
                      onChange={(on) => update.mutate({ key: e.key, production: on })}
                    />
                    <label htmlFor={`env-prod-${e.key}`} className="sr-only">
                      {e.name} is production
                    </label>
                  </td>
                  <td className={td}>
                    <Input
                      aria-label={`Snapshots kept in ${e.name}`}
                      type="number"
                      min={0}
                      className="h-7 w-20"
                      disabled={!canEdit}
                      defaultValue={e.snapshot_max_count}
                      onBlur={(ev) => {
                        // Empty or not a whole number: put the saved value back rather than save 0.
                        const n = ev.target.valueAsNumber;
                        if (ev.target.value.trim() === "" || !Number.isInteger(n) || n < 0) {
                          ev.target.value = String(e.snapshot_max_count);
                          return;
                        }
                        if (n !== e.snapshot_max_count) update.mutate({ key: e.key, snapshot_max_count: n });
                      }}
                    />
                  </td>
                  <td className={`${td} text-xs text-muted`}>
                    {e.projects ?? 0} projects, {e.deployments ?? 0} deployments
                  </td>
                  <td className={`${td} text-right`}>
                    {canEdit && (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Remove ${e.name}`}
                        disabled={(e.projects ?? 0) > 0 || (e.deployments ?? 0) > 0}
                        title={(e.projects ?? 0) > 0 ? "Projects deploy to it" : undefined}
                        onClick={() => remove.mutate(e.key)}
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
      {canEdit && (
        <form
          className="flex flex-wrap gap-2 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <Input
            aria-label="New environment key"
            placeholder="key"
            className="h-7 w-28 font-mono"
            value={f.key}
            onChange={(e) => setF({ ...f, key: e.target.value })}
          />
          <Input
            aria-label="New environment name"
            placeholder="Name"
            className="h-7 w-48"
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
          <Button size="sm" type="submit" disabled={!f.key || !f.name || add.isPending}>
            Add environment
          </Button>
        </form>
      )}
    </Panel>
  );
}

const globalRoles = [
  { value: "admin", label: "Administrator" },
  { value: "catalog-admin", label: "Catalog admin" },
  { value: "auditor", label: "Auditor" },
];
const projectRoles = [
  { value: "project-admin", label: "Project admin" },
  { value: "member", label: "Member" },
  { value: "viewer", label: "Viewer" },
];

function GroupMappings({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const mappings = useQuery(adm.groupMappings());
  const projects = useQuery(adm.projects("", false));
  const [f, setF] = useState({ group: "", project: "none", role: "auditor" });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["admin", "group-mappings"] });
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
      setF({ ...f, group: "" });
    },
    onError: (e) => failed(e, "Not mapped"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/group-mappings/{id}", { params: { path: { id } } })),
    onSuccess: refresh,
    onError: (e) => failed(e, "Not removed"),
  });
  const roles = f.project === "none" ? globalRoles : projectRoles;
  return (
    <Panel title="OIDC group mappings" flush>
      {mappings.isPending && <SkeletonRows rows={3} cols={3} />}
      {mappings.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Group
                </th>
                <th scope="col" className={th}>
                  Gives
                </th>
                <th scope="col" className={th}>
                  People in it
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {mappings.data.items.map((m) => (
                <tr key={m.id}>
                  <td className={`${td} pl-4 font-mono text-xs`}>{m.oidc_group}</td>
                  <td className={`${td} text-xs`}>
                    {[...globalRoles, ...projectRoles].find((r) => r.value === m.role)?.label ?? m.role}
                    {m.project ? ` in ${m.project.name}` : ""}
                  </td>
                  <td className={`${td} text-xs text-muted`}>{m.users ?? 0}</td>
                  <td className={`${td} text-right`}>
                    {canEdit && (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Remove the mapping of ${m.oidc_group}`}
                        onClick={() => remove.mutate(m.id)}
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
      {canEdit && (
        <form
          className="flex flex-wrap items-end gap-2 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <Input
            aria-label="OIDC group"
            placeholder="rvav-web"
            className="h-7 w-44 font-mono"
            value={f.group}
            onChange={(e) => setF({ ...f, group: e.target.value })}
          />
          <Select
            aria-label="In project"
            value={f.project}
            onChange={(v) => setF({ ...f, project: v, role: v === "none" ? "auditor" : "member" })}
            options={[
              { value: "none", label: "Everywhere (a global role)" },
              ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
            ]}
            className="w-56"
          />
          <Select
            aria-label="Role"
            value={f.role}
            onChange={(v) => setF({ ...f, role: v })}
            options={roles}
            className="w-40"
          />
          <Button size="sm" type="submit" disabled={!f.group || add.isPending}>
            Map group
          </Button>
        </form>
      )}
    </Panel>
  );
}

export function ProjectsPage() {
  const { me } = useCurrentProject();
  const isAdmin = me.global_roles.includes("admin");
  const seesAll = isAdmin || me.global_roles.includes("auditor");
  return (
    <Page>
      <PageHeader title="Projects" meta="Projects, who is in them and the environments they deploy to." />
      <Projects />
      {seesAll && <Environments canEdit={isAdmin} />}
      {seesAll && <GroupMappings canEdit={isAdmin} />}
    </Page>
  );
}
