// Who may request a catalog item (G1.2, ADR-0019): the projects it is
// shared with, and in each all their members or chosen users, OIDC groups
// and project roles.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
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
import { formatList } from "@/lib/format";
import { useDebounced } from "@/lib/hooks";
import { i18n } from "@/lib/i18n";
import { roleName } from "@/lib/i18n/names";
import { keys } from "@/lib/query";
import { cn } from "@/lib/utils";

/** The share that applies in every project, those made later included. */
const EVERY = "*";

const tr = i18n.getFixedT(null, "admin-catalog");
const userNoteId = "share-user-note";
const groupNoteId = "share-group-note";

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
  if (!named(a)) return tr("sharing.allMembers");
  const parts = [
    ...a!.user_ids.map((id) => users.find((u) => u.id === id)?.name ?? id),
    ...a!.groups.map((g) => tr("sharing.group", { name: g })),
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
  const { t } = useTranslation("admin-catalog");
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
      toast.success(t("sharing.projectsSaved"));
    },
    onError: (e) => failed(e, t("sharing.savingFailed")),
  });
  const projects = item.entitlements.projects ?? [];
  return (
    <Panel
      title={t("sharing.projects")}
      description={t("sharing.projectsDescription")}
      footer={
        <Button variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? t("sharing.saving") : t("sharing.saveProjects")}
        </Button>
      }
    >
      <div className="flex max-w-xl flex-col gap-3 text-body">
        <div role="radiogroup" aria-label={t("sharing.openTo")} className="flex flex-col gap-1.5">
          {[
            { v: true, label: t("sharing.everyProject"), hint: t("sharing.everyProjectHint") },
            { v: false, label: t("sharing.chosenProjects"), hint: "" },
          ].map((o) => (
            <label key={o.label} className="flex items-center gap-2">
              <input type="radio" name="open-to" checked={all === o.v} onChange={() => setAll(o.v)} />
              {o.label}
              {o.hint && <span className="text-secondary text-faint">{o.hint}</span>}
            </label>
          ))}
        </div>
        {all && chosen.size > 0 && (
          <p className="text-secondary text-muted">{t("sharing.ownShares", { count: chosen.size })}</p>
        )}
        {!all && (
          <ul aria-label={t("sharing.projects")} className="flex flex-col gap-1 rounded-md border border-line p-2">
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
    ? [{ key: EVERY, name: tr("sharing.everyProject"), audience: e.everywhere ?? nobody() }]
    : [];
  for (const id of e.project_ids) {
    const p = projects.find((x) => x.id === id);
    const a = e.audiences?.find((x) => x.project_id === id);
    rows.push({ key: id, name: p?.name ?? id, code: p?.code, audience: a ?? nobody() });
  }
  return rows;
}

function AudiencesPanel({ item }: { item: AdminCatalogItem }) {
  const { t } = useTranslation("admin-catalog");
  const e = item.entitlements;
  const [editing, setEditing] = useState<string | null>(null);
  const rows = sharesOf(e);
  const columns: Column<Share>[] = [
    {
      id: "project",
      header: t("sharing.project"),
      cell: (s) => (
        <span className="flex items-baseline gap-2">
          {s.name}
          {s.code && <span className="font-mono text-caption text-faint">{s.code}</span>}
        </span>
      ),
    },
    {
      id: "who",
      header: t("sharing.whoMay"),
      cell: (s) => (
        <span className={named(s.audience) ? "" : "text-muted"}>
          {audienceText(s.audience, e.users ?? [], e.roles ?? [])}
        </span>
      ),
    },
  ];
  return (
    <Panel
      title={t("sharing.whoMay")}
      description={t("sharing.whoMayDescription")}
      actions={<Button onClick={() => setEditing("")}>{t("sharing.share")}</Button>}
      flush
    >
      <DataTable
        label={t("sharing.shares")}
        columns={columns}
        rows={rows}
        rowKey={(s) => s.key}
        rowMenuLabel={(s) => t("sharing.actionsFor", { name: s.name })}
        rowMenu={(s) => <MenuItem onSelect={() => setEditing(s.key)}>{t("sharing.changeWho")}</MenuItem>}
        empty={<EmptyState title={t("sharing.notShared")} body={t("sharing.notSharedBody")} />}
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
  const { t } = useTranslation("admin-catalog");
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
  const projectName = (e.projects ?? []).find((p) => p.id === project)?.name ?? project;
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
      toast.success(
        project === EVERY ? t("sharing.sharedEverywhere") : t("sharing.sharedIn", { project: projectName }),
      );
      onClose();
    },
    onError: (err, { prefix, sent }) => {
      const { placed, unplaced } = shareErrors(isApiError(err) ? err.fieldErrors : [], prefix, sent);
      setErrors(placed);
      if (!isApiError(err) || unplaced.length > 0 || err.fieldErrors.length === 0)
        failed(err, t("sharing.sharingFailed"));
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
    ...(e.all_projects ? [{ value: EVERY, label: t("sharing.everyProject") }] : []),
    ...(e.projects ?? []).map((p) => ({ value: p.id, label: p.name, meta: p.code })),
  ];
  const note = (name: string, message: string) => t("sharing.note", { name, message });
  const userNote = Object.entries(errors.users).map(([id, m]) => note(users.find((u) => u.id === id)?.name ?? id, m));
  const groupNote = Object.entries(errors.groups).map(([g, m]) => (g.trim() ? note(g, m) : m));
  const roleNote = Object.entries(errors.roles).map(([k, m]) =>
    note((e.roles ?? []).find((r) => r.key === k)?.name ?? k, m),
  );
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("sharing.shareTitle", { name: item.name })}
      description={t("sharing.shareDescription")}
      onSubmit={share}
      submitLabel={t("sharing.share")}
      submitDisabled={!project || (chosen && !named(audience))}
      pending={save.isPending}
    >
      <Field
        label={t("sharing.project")}
        htmlFor="share-project"
        error={errors.project}
        hint={project === EVERY ? t("sharing.projectEveryHint") : undefined}
      >
        <Select
          id="share-project"
          value={project}
          onChange={pick}
          options={options}
          placeholder={t("sharing.chooseProject")}
        />
      </Field>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1 text-secondary font-medium text-fg">{t("sharing.whoMay")}</legend>
        {[
          { v: false, label: t("sharing.allMembers"), hint: t("sharing.allMembersHint") },
          { v: true, label: t("sharing.chosenPeople"), hint: t("sharing.chosenPeopleHint") },
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
              label={t("sharing.addPerson")}
              id="share-user"
              onPick={(u) => {
                edited();
                setUsers((l) => (l.some((x) => x.id === u.id) ? l : [...l, u]));
              }}
            />
            {users.length > 0 && (
              <ul ref={peopleList} aria-label={t("sharing.people")} className="flex flex-wrap gap-1.5">
                {users.map((u, i) => (
                  <Chip
                    key={u.id}
                    label={u.name}
                    remove={t("sharing.remove", { name: u.name })}
                    error={errors.users[u.id] ? userNoteId : undefined}
                    onRemove={() => {
                      edited();
                      refocus(peopleList.current, i, "share-user");
                      setUsers(users.filter((x) => x.id !== u.id));
                    }}
                  />
                ))}
              </ul>
            )}
            <FieldNote id={userNoteId} error={userNote.join(" ") || undefined} />
          </div>
          <div className="flex flex-col gap-2">
            <Field
              label={t("sharing.oidcGroup")}
              htmlFor="share-group"
              error={groupNote.join(" ") || undefined}
              hint={t("sharing.oidcGroupHint")}
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
                {t("sharing.addGroup")}
              </Button>
            </div>
            {groups.length > 0 && (
              <ul ref={groupList} aria-label={t("sharing.groups")} className="flex flex-wrap gap-1.5">
                {groups.map((g, i) => (
                  <Chip
                    key={g}
                    label={g}
                    remove={t("sharing.removeGroup", { name: g })}
                    error={errors.groups[g] ? groupNoteId : undefined}
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
            <legend className="mb-1 text-secondary font-medium text-fg">{t("sharing.projectRoles")}</legend>
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
                    {roleName(r)}
                  </label>
                </div>
              ))}
            </div>
            <FieldNote
              id="share-roles-note"
              error={roleNote.join(" ") || undefined}
              hint={t("sharing.projectRolesHint")}
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
      {project === EVERY && <p className="text-secondary text-muted">{t("sharing.pickOne")}</p>}
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
  const { t } = useTranslation("admin-catalog");
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
        {t("sharing.whoCan")}
      </h3>
      <div aria-live="polite" className="flex flex-col gap-2 text-secondary">
        {who.isError && <p className="text-bad">{t("sharing.whoCanFailed")}</p>}
        {who.data && <Requesters r={who.data} chosen={chosen} users={users} roles={roles} />}
        {!who.data && !who.isError && <p className="text-muted">{t("sharing.whoCanLoading")}</p>}
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
  const { t } = useTranslation("admin-catalog");
  const why = (p: CatalogRequesters["people"][number]) =>
    [
      p.named ? t("sharing.why.named") : "",
      ...p.groups.map((g) => t("sharing.why.in", { group: g })),
      ...p.roles.map(roleName),
      chosen && p.everywhere ? t("sharing.why.everywhere") : "",
    ]
      .filter(Boolean)
      .join(", ");
  return (
    <>
      <p>
        {r.people.length === 0
          ? t("sharing.nobodyCan", { project: r.project.name })
          : t("sharing.peopleCan", { count: r.people.length, project: r.project.name })}
      </p>
      {r.everywhere && chosen && (
        <p className="text-muted">
          {t("sharing.alsoEverywhere", {
            who: named(r.everywhere) ? audienceText(r.everywhere, users, roles) : t("sharing.allMembersLower"),
          })}
        </p>
      )}
      {r.people.length > 0 && (
        <ul aria-label={t("sharing.canRequest")} className="flex flex-col gap-0.5">
          {r.people.map((p) => (
            <li key={p.user.id}>
              {p.user.name}
              {why(p) && <span className="text-muted"> · {why(p)}</span>}
            </li>
          ))}
        </ul>
      )}
      {r.left_out.length > 0 && (
        <p className="text-muted">{t("sharing.leftOut", { names: formatList(r.left_out.map((u) => u.name)) })}</p>
      )}
      {r.not_requesters.length > 0 && (
        <p className="text-warn">
          {t("sharing.notRequesters", {
            names: formatList(r.not_requesters.map((u) => u.name)),
            project: r.project.name,
          })}
        </p>
      )}
      {r.groups
        .filter((g) => g.seen === 0)
        .map((g) => (
          <p key={g.name} className="text-warn">
            {t("sharing.unseenGroup", { group: g.name })}
          </p>
        ))}
    </>
  );
}
