// Who may request a catalog item (G1.2, ADR-0019): the projects it is
// shared with, and in each all their members or chosen users, OIDC groups
// and project roles.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldNote, Input } from "@/components/ui/input";
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
  FieldError,
  RoleRef,
  UserRef,
} from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { keys } from "@/lib/query";
import { cn } from "@/lib/utils";

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
  // Projects keep their own share under Every project: shares add up, and the server drops what project_ids leaves out.
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", {
          params: { path: { itemId: item.id } },
          body: { all_projects: all, project_ids: [...chosen] },
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
        {all && chosen.size > 0 && (
          <p className="text-secondary text-muted">
            {chosen.size === 1 ? "1 project keeps its own share" : `${chosen.size} projects keep their own share`},
            which adds to every project&apos;s.
          </p>
        )}
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

/** What a refused share says, by the field it is about: the server's pointers into the audience sent. */
interface ShareErrors {
  project?: string;
  users: Record<string, string>;
  groups: Record<string, string>;
  roles: Record<string, string>;
}

const noErrors = (): ShareErrors => ({ users: {}, groups: {}, roles: {} });

/** Maps a 422's pointers at the audience sent (under prefix) to its people, groups and roles; what's left is unplaced. */
export function shareErrors(
  errors: FieldError[],
  prefix: string,
  sent: CatalogAudience,
): { placed: ShareErrors; unplaced: FieldError[] } {
  const placed = noErrors();
  const unplaced: FieldError[] = [];
  for (const e of errors) {
    const m = e.field.startsWith(prefix + "/")
      ? /^\/(user_ids|groups|roles)\/(\d+)$/.exec(e.field.slice(prefix.length))
      : null;
    const value = m ? sent[m[1] as "user_ids" | "groups" | "roles"][Number(m[2])] : undefined;
    if (m && value !== undefined) {
      const list = m[1] === "user_ids" ? placed.users : m[1] === "groups" ? placed.groups : placed.roles;
      list[value] ??= e.message;
    } else if (e.field === `${prefix}/project_id` || e.field === "/project_ids" || e.field === "/everywhere")
      placed.project ??= e.message;
    else unplaced.push(e);
  }
  return { placed, unplaced };
}

/** Puts the focus back in a list of chips once one is removed: on the next, or on fallback when none is left. */
function useChipFocus() {
  const want = useRef<{ list: HTMLElement | null; index: number; fallback: string } | null>(null);
  useLayoutEffect(() => {
    const w = want.current;
    if (!w) return;
    want.current = null;
    const buttons = w.list?.isConnected ? [...w.list.querySelectorAll<HTMLButtonElement>("button")] : [];
    (buttons[Math.min(w.index, buttons.length - 1)] ?? document.getElementById(w.fallback))?.focus();
  });
  return (list: HTMLElement | null, index: number, fallback: string) => {
    want.current = { list, index, fallback };
  };
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
  const [errors, setErrors] = useState(noErrors);
  const peopleList = useRef<HTMLUListElement>(null);
  const groupList = useRef<HTMLUListElement>(null);
  const refocus = useChipFocus();
  const edited = () => setErrors(noErrors());
  const pick = (key: string) => {
    const a = audienceIn(key);
    edited();
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
    if (g && !groups.includes(g)) {
      edited();
      setGroups([...groups, g]);
    }
    setGroup("");
  };

  const save = useMutation({
    mutationFn: ({ body }: { body: CatalogEntitlements; prefix: string; sent: CatalogAudience }) =>
      call(api.PUT("/admin/catalog/items/{itemId}/entitlements", { params: { path: { itemId: item.id } }, body })),
    onSuccess: () => {
      refresh();
      toast.success(`Shared in ${projectName}`);
      onClose();
    },
    onError: (err, { prefix, sent }) => {
      const { placed, unplaced } = shareErrors(isApiError(err) ? err.fieldErrors : [], prefix, sent);
      setErrors(placed);
      if (!isApiError(err) || unplaced.length > 0 || err.fieldErrors.length === 0) failed(err, "Sharing failed");
      const first =
        (placed.project && "share-project") ||
        (Object.keys(placed.users).length > 0 && "share-user") ||
        (Object.keys(placed.groups).length > 0 && "share-group") ||
        (sent.roles.find((k) => placed.roles[k]) && `share-role-${sent.roles.find((k) => placed.roles[k])}`);
      if (first) document.getElementById(first)?.focus();
    },
  });
  const share = () => {
    const others = (e.audiences ?? []).filter((a) => a.project_id !== project);
    const audiences = named(audience) ? [...others, { project_id: project, ...audience }] : others;
    const body: CatalogEntitlements =
      project === EVERY
        ? { all_projects: true, project_ids: e.project_ids, everywhere: audience, audiences: e.audiences ?? [] }
        : {
            all_projects: e.all_projects,
            project_ids: e.project_ids.includes(project) ? e.project_ids : [...e.project_ids, project],
            audiences,
          };
    save.mutate({ body, prefix: project === EVERY ? "/everywhere" : `/audiences/${others.length}`, sent: audience });
  };

  const options = [
    ...(e.all_projects ? [{ value: EVERY, label: "Every project" }] : []),
    ...(e.projects ?? []).map((p) => ({ value: p.id, label: p.name, meta: p.code })),
  ];
  const userNote = Object.entries(errors.users).map(([id, m]) => `${users.find((u) => u.id === id)?.name ?? id}: ${m}`);
  const groupNote = Object.entries(errors.groups).map(([g, m]) => (g.trim() ? `${g}: ${m}` : m));
  const roleNote = Object.entries(errors.roles).map(
    ([k, m]) => `${(e.roles ?? []).find((r) => r.key === k)?.name ?? k}: ${m}`,
  );
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Share ${item.name}`}
      description="Pick a project, then who may request the item there."
      onSubmit={share}
      submitLabel="Share"
      submitDisabled={!project || (chosen && !named(audience))}
      pending={save.isPending}
    >
      <Field
        label="Project"
        htmlFor="share-project"
        error={errors.project}
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
            <input
              type="radio"
              name="share-who"
              checked={chosen === o.v}
              onChange={() => {
                edited();
                setChosen(o.v);
              }}
            />
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
              onPick={(u) => {
                edited();
                setUsers((l) => (l.some((x) => x.id === u.id) ? l : [...l, u]));
              }}
            />
            {users.length > 0 && (
              <ul ref={peopleList} aria-label="People" className="flex flex-wrap gap-1.5">
                {users.map((u, i) => (
                  <Chip
                    key={u.id}
                    label={u.name}
                    remove={`Remove ${u.name}`}
                    error={errors.users[u.id] ? "share-user-note" : undefined}
                    onRemove={() => {
                      edited();
                      refocus(peopleList.current, i, "share-user");
                      setUsers(users.filter((x) => x.id !== u.id));
                    }}
                  />
                ))}
              </ul>
            )}
            <FieldNote id="share-user-note" error={userNote.join(" ") || undefined} />
          </div>
          <div className="flex flex-col gap-2">
            <Field
              label="OIDC group"
              htmlFor="share-group"
              error={groupNote.join(" ") || undefined}
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
              <ul ref={groupList} aria-label="Groups" className="flex flex-wrap gap-1.5">
                {groups.map((g, i) => (
                  <Chip
                    key={g}
                    label={g}
                    remove={`Remove group ${g}`}
                    error={errors.groups[g] ? "share-group-note" : undefined}
                    onRemove={() => {
                      edited();
                      refocus(groupList.current, i, "share-group");
                      setGroups(groups.filter((x) => x !== g));
                    }}
                  />
                ))}
              </ul>
            )}
          </div>
          <fieldset className="flex flex-col gap-1" aria-describedby="share-roles-note">
            <legend className="mb-1 text-secondary font-medium text-fg">Project roles</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
              {(e.roles ?? []).map((r) => (
                <div key={r.key} className="flex items-center gap-1.5">
                  <Checkbox
                    id={`share-role-${r.key}`}
                    checked={roles.includes(r.key)}
                    aria-describedby={errors.roles[r.key] ? "share-roles-note" : undefined}
                    onChange={(on) => {
                      edited();
                      setRoles(on ? [...roles, r.key] : roles.filter((k) => k !== r.key));
                    }}
                  />
                  <label htmlFor={`share-role-${r.key}`} className="text-body">
                    {r.name}
                  </label>
                </div>
              ))}
            </div>
            <FieldNote
              id="share-roles-note"
              error={roleNote.join(" ") || undefined}
              hint="Whoever holds one in the project, themselves or through a group."
            />
          </fieldset>
        </>
      )}
      {project && project !== EVERY && (
        <WhoCanRequest
          itemId={item.id}
          audience={{ project_id: project, ...audience }}
          chosen={chosen}
          users={e.users ?? []}
          roles={e.roles ?? []}
        />
      )}
      {project === EVERY && (
        <p className="text-secondary text-muted">Who can request is shown for one project at a time: pick one.</p>
      )}
    </Sheet>
  );
}

function Chip({
  label,
  remove,
  error,
  onRemove,
}: {
  label: string;
  remove: string;
  /** The ID of the note saying what is wrong with it. */
  error?: string;
  onRemove: () => void;
}) {
  return (
    <li
      className={cn(
        "flex items-center gap-1 rounded-md border bg-sunken py-0.5 pr-0.5 pl-2 text-secondary",
        error ? "border-bad text-bad" : "border-line",
      )}
    >
      {label}
      <IconButton type="button" label={remove} aria-describedby={error} className="size-6" onClick={onRemove}>
        <X />
      </IconButton>
    </li>
  );
}

/** The project's members the audience lets request, and those it leaves out, before saving. */
function WhoCanRequest({
  itemId,
  audience,
  chosen,
  users,
  roles,
}: {
  itemId: string;
  audience: CatalogProjectAudience;
  chosen: boolean;
  users: UserRef[];
  roles: RoleRef[];
}) {
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
        {who.data && <Requesters r={who.data} chosen={chosen} users={users} roles={roles} />}
        {!who.data && !who.isError && <p className="text-muted">Working out who can request…</p>}
      </div>
    </section>
  );
}

function Requesters({
  r,
  chosen,
  users,
  roles,
}: {
  r: CatalogRequesters;
  chosen: boolean;
  users: UserRef[];
  roles: RoleRef[];
}) {
  const why = (p: CatalogRequesters["people"][number]) =>
    [
      p.named ? "named" : "",
      ...p.groups.map((g) => `in ${g}`),
      ...p.roles.map((x) => x.name),
      chosen && p.everywhere ? "shared with every project" : "",
    ]
      .filter(Boolean)
      .join(", ");
  return (
    <>
      <p>
        {r.people.length === 0
          ? `Nobody in ${r.project.name} can request it.`
          : `${r.people.length} ${r.people.length === 1 ? "person" : "people"} in ${r.project.name} can request it.`}
      </p>
      {r.everywhere && chosen && (
        <p className="text-muted">
          Also shared with every project:{" "}
          {named(r.everywhere) ? audienceText(r.everywhere, users, roles) : "all members"}. Shares add up.
        </p>
      )}
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
