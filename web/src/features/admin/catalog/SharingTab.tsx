// Who may request a catalog item (G1.2, ADR-0019): the projects it is
// shared with, and in each all their members or chosen users, OIDC groups
// and project roles.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { UserPicker } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type {
  AdminCatalogItem,
  CatalogAudience,
  CatalogEntitlements,
  CatalogProjectAudience,
  CatalogRequesters,
  RoleRef,
  UserRef,
} from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { keys } from "@/lib/query";

/** The share that applies in every project, those made later included. */
const EVERY = "*";

const nobody = (): CatalogAudience => ({ user_ids: [], groups: [], roles: [] });
const named = (a: CatalogAudience | undefined) => !!a && a.user_ids.length + a.groups.length + a.roles.length > 0;

function useRefresh(itemId: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
    void qc.invalidateQueries({ queryKey: keys.catalog.all });
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.item(itemId) });
  };
}

function failed(e: unknown, fallback: string) {
  toast.error(isApiError(e) ? e.problem.title : fallback, {
    description: isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail) : undefined,
  });
}

/** Who an audience names, in a sentence: all members when nobody. */
export function audienceText(a: CatalogAudience | undefined, users: UserRef[], roles: RoleRef[]): string {
  if (!named(a)) return "All members";
  const parts = [
    ...a!.user_ids.map((id) => users.find((u) => u.id === id)?.name ?? id),
    ...a!.groups.map((g) => `group ${g}`),
    ...a!.roles.map((k) => roles.find((r) => r.key === k)?.name ?? k),
  ];
  return parts.join(", ");
}

export function SharingTab({ item }: { item: AdminCatalogItem }) {
  return (
    <div className="flex flex-col gap-4">
      {/* Drawn anew once saved; the shares' panel stays, so the focus goes back to what opened its sheet. */}
      <ProjectsPanel
        key={JSON.stringify([item.entitlements.all_projects, item.entitlements.project_ids])}
        item={item}
      />
      <AudiencesPanel item={item} />
    </div>
  );
}

function ProjectsPanel({ item }: { item: AdminCatalogItem }) {
  const refresh = useRefresh(item.id);
  const [all, setAll] = useState(item.entitlements.all_projects);
  const [chosen, setChosen] = useState(new Set(item.entitlements.project_ids));
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", {
          params: { path: { itemId: item.id } },
          body: { all_projects: all, project_ids: all ? [] : [...chosen] },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success("Projects saved");
    },
    onError: (e) => failed(e, "Saving failed"),
  });
  const projects = item.entitlements.projects ?? [];
  return (
    <Panel
      title="Projects"
      description="The projects whose catalog has it. Who may request it in each is below."
      footer={
        <Button variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : "Save projects"}
        </Button>
      }
    >
      <div className="flex max-w-xl flex-col gap-3 text-body">
        <div role="radiogroup" aria-label="Open to" className="flex flex-col gap-1.5">
          {[
            { v: true, label: "Every project", hint: "projects made later included" },
            { v: false, label: "Chosen projects", hint: "" },
          ].map((o) => (
            <label key={o.label} className="flex items-center gap-2">
              <input type="radio" name="open-to" checked={all === o.v} onChange={() => setAll(o.v)} />
              {o.label}
              {o.hint && <span className="text-secondary text-faint">{o.hint}</span>}
            </label>
          ))}
        </div>
        {!all && (
          <ul aria-label="Projects" className="flex flex-col gap-1 rounded-md border border-line p-2">
            {projects.map((p) => (
              <li key={p.id} className="flex min-h-7 items-center gap-2">
                <Checkbox
                  id={`project-${p.id}`}
                  checked={chosen.has(p.id)}
                  onChange={(on) =>
                    setChosen((s) => {
                      const n = new Set(s);
                      if (on) n.add(p.id);
                      else n.delete(p.id);
                      return n;
                    })
                  }
                />
                <label htmlFor={`project-${p.id}`}>{p.name}</label>
                <span className="font-mono text-caption text-faint">{p.code}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}

interface Share {
  key: string;
  name: string;
  code?: string;
  audience: CatalogAudience;
}

function sharesOf(e: CatalogEntitlements): Share[] {
  const projects = e.projects ?? [];
  const rows: Share[] = e.all_projects
    ? [{ key: EVERY, name: "Every project", audience: e.everywhere ?? nobody() }]
    : [];
  for (const id of e.project_ids) {
    const p = projects.find((x) => x.id === id);
    const a = e.audiences?.find((x) => x.project_id === id);
    rows.push({ key: id, name: p?.name ?? id, code: p?.code, audience: a ?? nobody() });
  }
  return rows;
}

function AudiencesPanel({ item }: { item: AdminCatalogItem }) {
  const e = item.entitlements;
  const [editing, setEditing] = useState<string | null>(null);
  const rows = sharesOf(e);
  const columns: Column<Share>[] = [
    {
      id: "project",
      header: "Project",
      cell: (s) => (
        <span className="flex items-baseline gap-2">
          {s.name}
          {s.code && <span className="font-mono text-caption text-faint">{s.code}</span>}
        </span>
      ),
    },
    {
      id: "who",
      header: "Who may request it",
      cell: (s) => (
        <span className={named(s.audience) ? "" : "text-muted"}>
          {audienceText(s.audience, e.users ?? [], e.roles ?? [])}
        </span>
      ),
    },
  ];
  return (
    <Panel
      title="Who may request it"
      description="In each project, all the members who may request there, or only the users, groups and roles you choose. Shares add up."
      actions={<Button onClick={() => setEditing("")}>Share</Button>}
      flush
    >
      <DataTable
        label="Shares"
        columns={columns}
        rows={rows}
        rowKey={(s) => s.key}
        rowMenuLabel={(s) => `Actions for ${s.name}`}
        rowMenu={(s) => <MenuItem onSelect={() => setEditing(s.key)}>Change who may request it</MenuItem>}
        empty={
          <EmptyState
            title="Not shared"
            body="No project's catalog has it: nobody can request it. Share it with a project."
          />
        }
      />
      {editing !== null && <ShareSheet item={item} initial={editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

function ShareSheet({ item, initial, onClose }: { item: AdminCatalogItem; initial: string; onClose: () => void }) {
  const e = item.entitlements;
  const refresh = useRefresh(item.id);
  const audienceIn = (key: string) =>
    key === EVERY ? (e.everywhere ?? nobody()) : (e.audiences?.find((a) => a.project_id === key) ?? nobody());
  const start = audienceIn(initial);
  const [project, setProject] = useState(initial);
  const [chosen, setChosen] = useState(named(start));
  const [users, setUsers] = useState<UserRef[]>(
    start.user_ids.map((id) => (e.users ?? []).find((u) => u.id === id) ?? { id, name: id, email: "" }),
  );
  const [groups, setGroups] = useState(start.groups);
  const [roles, setRoles] = useState(start.roles);
  const [group, setGroup] = useState("");
  const pick = (key: string) => {
    const a = audienceIn(key);
    setProject(key);
    setChosen(named(a));
    setUsers(a.user_ids.map((id) => (e.users ?? []).find((u) => u.id === id) ?? { id, name: id, email: "" }));
    setGroups(a.groups);
    setRoles(a.roles);
  };
  const audience: CatalogAudience = chosen ? { user_ids: users.map((u) => u.id), groups, roles } : nobody();
  const projectName =
    project === EVERY ? "every project" : ((e.projects ?? []).find((p) => p.id === project)?.name ?? project);
  const addGroup = () => {
    const g = group.trim();
    if (g && !groups.includes(g)) setGroups([...groups, g]);
    setGroup("");
  };

  const save = useMutation({
    mutationFn: () => {
      const others = (e.audiences ?? []).filter((a) => a.project_id !== project);
      const body: CatalogEntitlements =
        project === EVERY
          ? { all_projects: true, project_ids: e.project_ids, everywhere: audience, audiences: e.audiences ?? [] }
          : {
              all_projects: e.all_projects,
              project_ids: e.project_ids.includes(project) ? e.project_ids : [...e.project_ids, project],
              audiences: named(audience) ? [...others, { project_id: project, ...audience }] : others,
            };
      return call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", { params: { path: { itemId: item.id } }, body }),
      );
    },
    onSuccess: () => {
      refresh();
      toast.success(`Shared in ${projectName}`);
      onClose();
    },
    onError: (err) => failed(err, "Sharing failed"),
  });

  const options = [
    ...(e.all_projects ? [{ value: EVERY, label: "Every project" }] : []),
    ...(e.projects ?? []).map((p) => ({ value: p.id, label: p.name, meta: p.code })),
  ];
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Share ${item.name}`}
      description="Pick a project, then who may request the item there."
      onSubmit={() => save.mutate()}
      submitLabel="Share"
      submitDisabled={!project || (chosen && !named(audience))}
      pending={save.isPending}
    >
      <Field
        label="Project"
        htmlFor="share-project"
        hint={project === EVERY ? "Who may request it in each project, those made later included." : undefined}
      >
        <Select id="share-project" value={project} onChange={pick} options={options} placeholder="Choose a project" />
      </Field>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1 text-secondary font-medium text-fg">Who may request it</legend>
        {[
          { v: false, label: "All members", hint: "everyone who may request in the project" },
          { v: true, label: "Chosen people", hint: "the users, groups and roles below" },
        ].map((o) => (
          <label key={o.label} className="flex items-center gap-2 text-body">
            <input type="radio" name="share-who" checked={chosen === o.v} onChange={() => setChosen(o.v)} />
            {o.label}
            <span className="text-secondary text-faint">{o.hint}</span>
          </label>
        ))}
      </fieldset>
      {chosen && (
        <>
          <div className="flex flex-col gap-2">
            <UserPicker
              label="Add a person"
              id="share-user"
              onPick={(u) => setUsers((l) => (l.some((x) => x.id === u.id) ? l : [...l, u]))}
            />
            {users.length > 0 && (
              <ul aria-label="People" className="flex flex-wrap gap-1.5">
                {users.map((u) => (
                  <Chip
                    key={u.id}
                    label={u.name}
                    remove={`Remove ${u.name}`}
                    onRemove={() => setUsers(users.filter((x) => x.id !== u.id))}
                  />
                ))}
              </ul>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Field
              label="OIDC group"
              htmlFor="share-group"
              hint="As the identity provider's token names it. Press Enter to add it."
            >
              <Input
                id="share-group"
                value={group}
                onChange={(ev) => setGroup(ev.target.value)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter") {
                    ev.preventDefault();
                    addGroup();
                  }
                }}
                autoComplete="off"
              />
            </Field>
            <div>
              <Button type="button" disabled={!group.trim()} onClick={addGroup}>
                Add group
              </Button>
            </div>
            {groups.length > 0 && (
              <ul aria-label="Groups" className="flex flex-wrap gap-1.5">
                {groups.map((g) => (
                  <Chip
                    key={g}
                    label={g}
                    remove={`Remove group ${g}`}
                    onRemove={() => setGroups(groups.filter((x) => x !== g))}
                  />
                ))}
              </ul>
            )}
          </div>
          <fieldset className="flex flex-col gap-1">
            <legend className="mb-1 text-secondary font-medium text-fg">Project roles</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
              {(e.roles ?? []).map((r) => (
                <div key={r.key} className="flex items-center gap-1.5">
                  <Checkbox
                    id={`share-role-${r.key}`}
                    checked={roles.includes(r.key)}
                    onChange={(on) => setRoles(on ? [...roles, r.key] : roles.filter((k) => k !== r.key))}
                  />
                  <label htmlFor={`share-role-${r.key}`} className="text-body">
                    {r.name}
                  </label>
                </div>
              ))}
            </div>
            <p className="text-secondary text-muted">
              Whoever holds one in the project, themselves or through a group.
            </p>
          </fieldset>
        </>
      )}
      {project && project !== EVERY && (
        <WhoCanRequest itemId={item.id} audience={{ project_id: project, ...audience }} />
      )}
      {project === EVERY && (
        <p className="text-secondary text-muted">Who can request is shown for one project at a time: pick one.</p>
      )}
    </Sheet>
  );
}

function Chip({ label, remove, onRemove }: { label: string; remove: string; onRemove: () => void }) {
  return (
    <li className="flex items-center gap-1 rounded-md border border-line bg-sunken py-0.5 pr-0.5 pl-2 text-secondary">
      {label}
      <IconButton type="button" label={remove} className="size-6" onClick={onRemove}>
        <X />
      </IconButton>
    </li>
  );
}

/** The project's members the audience lets request, and those it leaves out, before saving. */
function WhoCanRequest({ itemId, audience }: { itemId: string; audience: CatalogProjectAudience }) {
  const asked = useDebounced(audience, 300);
  const who = useQuery({
    queryKey: [...keys.catalogAdmin.all, "requesters", itemId, asked],
    queryFn: ({ signal }) =>
      call(
        api.POST("/admin/catalog/items/{itemId}/entitlements/requesters", {
          params: { path: { itemId } },
          body: asked,
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
  });
  return (
    <section aria-labelledby="who-can-request" className="flex flex-col gap-2 rounded-md border border-line p-3">
      <h3 id="who-can-request" className="text-secondary font-medium text-fg">
        Who can request it
      </h3>
      <div aria-live="polite" className="flex flex-col gap-2 text-secondary">
        {who.isError && <p className="text-bad">Who can request couldn't be worked out.</p>}
        {who.data && <Requesters r={who.data} />}
        {!who.data && !who.isError && <p className="text-muted">Working out who can request…</p>}
      </div>
    </section>
  );
}

function Requesters({ r }: { r: CatalogRequesters }) {
  const why = (p: CatalogRequesters["people"][number]) =>
    [p.named ? "named" : "", ...p.groups.map((g) => `in ${g}`), ...p.roles.map((x) => x.name)]
      .filter(Boolean)
      .join(", ");
  return (
    <>
      <p>
        {r.people.length === 0
          ? `Nobody in ${r.project.name} can request it.`
          : `${r.people.length} ${r.people.length === 1 ? "person" : "people"} in ${r.project.name} can request it.`}
      </p>
      {r.people.length > 0 && (
        <ul aria-label="Can request it" className="flex flex-col gap-0.5">
          {r.people.map((p) => (
            <li key={p.user.id}>
              {p.user.name}
              {why(p) && <span className="text-muted"> · {why(p)}</span>}
            </li>
          ))}
        </ul>
      )}
      {r.left_out.length > 0 && <p className="text-muted">Left out: {r.left_out.map((u) => u.name).join(", ")}.</p>}
      {r.not_requesters.length > 0 && (
        <p className="text-warn">
          {r.not_requesters.map((u) => u.name).join(", ")} may not request in {r.project.name}: give them a role that
          may.
        </p>
      )}
      {r.groups
        .filter((g) => g.seen === 0)
        .map((g) => (
          <p key={g.name} className="text-warn">
            Nobody rvav has seen is in {g.name} yet: check its name in the identity provider.
          </p>
        ))}
    </>
  );
}
