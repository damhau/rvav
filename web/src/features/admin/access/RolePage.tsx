// One role (ADR-0034): what it allows, in the catalogue's words, and a
// custom one's editor for those holding access.manage.
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis, TriangleAlert } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, Input, Textarea } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors } from "../governance/common";
import { allows, byArea, type Catalogue, inOrder, scopeOf, scopeText } from "./permissions";
import { AdminWarning, Refusal, refused } from "./refusal";
import { areaName, day2Group, day2Name, permissionSummary, roleName } from "@/lib/i18n/names";

type S = components["schemas"];

function Crumbs() {
  const { t } = useTranslation("shell");
  return (
    <Breadcrumbs>
      <Link to="/admin/roles">{t("nav.admin.roles")}</Link>
    </Breadcrumbs>
  );
}

const givenText = (scope: S["RoleScope"], why?: string) =>
  scope === "project"
    ? i18n.t("admin-access:role.givenProject")
    : scope === "rvav"
      ? why
        ? i18n.t("admin-access:role.givenRvavWhy", { why })
        : i18n.t("admin-access:role.givenRvav")
      : i18n.t("admin-access:role.givenAny");

/** What a role with these permissions allows and where it can be given: a built-in role's scope is its own. */
function Allows({
  catalogue,
  permissions,
  builtInScope,
}: {
  catalogue: Catalogue;
  permissions: string[];
  builtInScope?: S["RoleScope"];
}) {
  const { t } = useTranslation("admin-access");
  const sentences = allows(catalogue, permissions);
  const { scope, why } = builtInScope ? { scope: builtInScope, why: undefined } : scopeOf(catalogue, permissions);
  return (
    <div className="flex flex-col gap-3 text-body">
      {permissions.includes("access.manage") && <AdminWarning />}
      {sentences.length === 0 ? (
        <p className="text-secondary text-muted">{t("role.nothing")}</p>
      ) : (
        <ul className="flex list-disc flex-col gap-1 pl-4">
          {sentences.map((s) => (
            <li key={s.id}>{s.text}</li>
          ))}
        </ul>
      )}
      {sentences.length > 0 && <p className="text-secondary text-muted">{givenText(scope, why)}</p>}
    </div>
  );
}

function Uses({ role }: { role: S["Role"] }) {
  const { t } = useTranslation("admin-access");
  return (
    <p className="text-secondary text-muted">
      {role.holders === 0 ? t("role.givenToNobody") : t("role.givenTimes", { count: role.holders })}
      {role.policies.length > 0 && (
        <> {t("role.namedBy", { names: formatList(role.policies), count: role.policies.length })}</>
      )}
    </p>
  );
}

function PermissionBox({
  id,
  label,
  what,
  checked,
  disabled,
  onChange,
  mono,
}: {
  id: string;
  label: string;
  what?: React.ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (on: boolean) => void;
  mono?: boolean;
}) {
  const box = `perm-${id}`;
  return (
    <div className="flex items-start gap-2 py-1">
      <Checkbox
        id={box}
        className="mt-0.5"
        checked={checked}
        disabled={disabled}
        onChange={onChange}
        aria-describedby={what ? `${box}-what` : undefined}
      />
      <div className="flex min-w-0 flex-col gap-0.5">
        <label htmlFor={box} className={mono ? "font-mono text-secondary" : "text-body"}>
          {label}
        </label>
        {what && (
          <p id={`${box}-what`} className="text-secondary text-muted">
            {what}
          </p>
        )}
      </div>
    </div>
  );
}

/** The catalogue's permissions by area, then the day-2 ones by group or single action. */
function PermissionList({
  catalogue,
  value,
  onChange,
}: {
  catalogue: Catalogue;
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const { t } = useTranslation("admin-access");
  const held = new Set(value);
  const set = (id: string, on: boolean, drop: (p: string) => boolean = () => false) =>
    onChange(on ? [...value.filter((p) => !drop(p)), id] : value.filter((p) => p !== id));
  return (
    <div className="flex flex-col gap-4">
      {byArea(catalogue).map(([area, items]) => (
        <fieldset key={area} className="flex flex-col">
          <legend className="label mb-1">{areaName(area)}</legend>
          {items.map((p) => (
            <PermissionBox
              key={p.id}
              id={p.id}
              label={p.id}
              mono
              what={
                <>
                  {permissionSummary(p)}
                  {p.scope === "rvav" && (
                    <Badge tone="outline" className="ml-1.5">
                      {t("role.rvavOnlyBadge")}
                    </Badge>
                  )}
                </>
              }
              checked={held.has(p.id)}
              onChange={(on) => set(p.id, on)}
            />
          ))}
        </fieldset>
      ))}
      <fieldset className="flex flex-col gap-2">
        <legend className="label mb-1">{t("role.day2")}</legend>
        <p className="text-secondary text-muted">{t("role.day2Lead")}</p>
        {catalogue.day2.map((g) => {
          const all = held.has(g.permission);
          const prefix = `${g.permission}:`;
          const known = new Set(g.actions.map((a) => a.permission));
          const actions = [
            ...g.actions,
            ...value
              .filter((p) => p.startsWith(prefix) && !known.has(p))
              .map((p) => ({ key: p.slice(prefix.length), name: p.slice(prefix.length), permission: p })),
          ];
          return (
            <div key={g.group} className="flex flex-col">
              <PermissionBox
                id={g.permission}
                label={g.permission}
                mono
                what={t("role.everyAction", { group: day2Group(g.group) })}
                checked={all}
                onChange={(on) => set(g.permission, on, (p) => p.startsWith(prefix))}
              />
              {actions.length > 0 && (
                <div className="ml-6 grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                  {actions.map((a) => (
                    <PermissionBox
                      key={a.permission}
                      id={a.permission}
                      label={day2Name(a)}
                      checked={all || held.has(a.permission)}
                      disabled={all}
                      onChange={(on) => set(a.permission, on)}
                    />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </fieldset>
    </div>
  );
}

/** Why a custom role can't be deleted, said in place of Delete. */
export const cantDelete = (role: S["Role"]) =>
  role.holders > 0
    ? i18n.t("admin-access:roles.cantDeleteGiven", { count: role.holders })
    : role.policies.length > 0
      ? i18n.t("admin-access:roles.cantDeletePolicies")
      : undefined;

/** Asks before deleting a custom role, then deletes it. */
export function DeleteRole({
  role,
  onClose,
  onDeleted,
}: {
  role: S["Role"];
  onClose: () => void;
  /** Called once it is deleted; the role's page is forgotten once what it returns settles. */
  onDeleted?: () => Promise<unknown> | void;
}) {
  const { t } = useTranslation("admin-access");
  const qc = useQueryClient();
  const own = adm.role(role.key).queryKey;
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/roles/{key}", { params: { path: { key: role.key } } })),
    onSuccess: () => {
      // The role's own page isn't read again: it would show the role not found before it leaves.
      void qc.invalidateQueries({
        queryKey: ["admin"],
        predicate: (query) => JSON.stringify(query.queryKey) !== JSON.stringify(own),
      });
      void qc.invalidateQueries({ queryKey: ["me"] });
      toast.success(t("role.deleted", { name: roleName(role) }));
      onClose();
      void Promise.resolve(onDeleted?.()).then(() => qc.removeQueries({ queryKey: own, exact: true }));
    },
    onError: (e) => {
      onClose();
      failed(e, t("role.notDeleted"));
    },
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("role.deleteTitle", { name: roleName(role) })}
      description={t("role.deleteBody")}
      consequences={t("role.deleteConsequences")}
      confirmLabel={t("role.deleteRole")}
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
    />
  );
}

/** A custom role's editor, or a new role's; its submit button is in the page's header. */
function RoleForm({
  formId,
  catalogue,
  role,
  start,
}: {
  formId: string;
  catalogue: Catalogue;
  role?: S["Role"];
  start?: string[];
}) {
  const { t } = useTranslation("admin-access");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [f, setF] = useState({
    key: role?.key ?? "",
    name: role?.name ?? "",
    description: role?.description ?? "",
    permissions: role?.permissions ?? start ?? [],
  });
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ["admin"] });
    void qc.invalidateQueries({ queryKey: ["me"] });
  };
  const save = useMutation({
    mutationKey: saveKey,
    mutationFn: () => {
      const body = { name: f.name, description: f.description, permissions: inOrder(catalogue, f.permissions) };
      return role
        ? call(api.PATCH("/admin/roles/{key}", { params: { path: { key: role.key } }, body }))
        : call(api.POST("/admin/roles", { body: { ...body, key: f.key } }));
    },
    onSuccess: (r) => {
      changed();
      toast.success(role ? t("role.saved", { name: r.name }) : t("role.made", { name: r.name }));
      if (!role) void navigate({ to: "/admin/roles/$roleKey", params: { roleKey: r.key } });
    },
    onError: (e) => {
      if (!refused(e, [409, 422])) failed(e, t("role.notSaved"));
    },
  });
  const errors = fieldErrors(save.error);
  const keyTaken = !role && isApiError(save.error) && save.error.status === 409 ? save.error.problem.detail : undefined;
  const permissionsError = errorAt(errors, "/permissions");
  return (
    <form
      id={formId}
      className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_340px]"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex min-w-0 flex-col gap-5">
        <Panel title={t("role.panel")}>
          <div className="grid max-w-2xl grid-cols-1 gap-3 md:grid-cols-2">
            <Field
              label={t("role.key")}
              htmlFor="role-key"
              error={errors["/key"] ?? keyTaken}
              hint={role ? t("role.keyFixed") : t("role.keyHint")}
            >
              <Input
                id="role-key"
                className="font-mono"
                value={f.key}
                disabled={Boolean(role)}
                autoComplete="off"
                onChange={(e) => setF({ ...f, key: e.target.value })}
              />
            </Field>
            <Field label={t("role.name")} htmlFor="role-name" error={errors["/name"]}>
              <Input id="role-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
            </Field>
            <Field
              label={t("role.description")}
              htmlFor="role-description"
              error={errors["/description"]}
              className="md:col-span-2"
            >
              <Textarea
                id="role-description"
                rows={2}
                value={f.description}
                onChange={(e) => setF({ ...f, description: e.target.value })}
              />
            </Field>
          </div>
        </Panel>
        <Panel title={t("role.permissions")}>
          {permissionsError && (
            <p role="alert" className="mb-3 flex items-start gap-1.5 text-secondary text-bad">
              <TriangleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
              {permissionsError}
            </p>
          )}
          <PermissionList
            catalogue={catalogue}
            value={f.permissions}
            onChange={(permissions) => setF({ ...f, permissions })}
          />
        </Panel>
      </div>
      <div className="flex flex-col gap-5 lg:sticky lg:top-4 lg:self-start">
        <Panel title={t("role.allows")}>
          <Allows catalogue={catalogue} permissions={f.permissions} />
        </Panel>
        <div className="flex flex-col gap-2">
          {role && <Uses role={role} />}
          {!keyTaken && <Refusal error={save.error} only={[409]} />}
        </div>
      </div>
    </form>
  );
}

/** A role that can't be changed here: built in, or read by someone without access.manage. */
function RoleView({ catalogue, role, canMake }: { catalogue: Catalogue; role: S["Role"]; canMake: boolean }) {
  const { t } = useTranslation("admin-access");
  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
      <Panel title={t("role.allows")}>
        <div className="flex flex-col gap-3">
          {role.description && <p className="text-body">{role.description}</p>}
          <Allows
            catalogue={catalogue}
            permissions={role.permissions}
            builtInScope={role.built_in ? role.scope : undefined}
          />
          {role.permissions.length > 0 && (
            <ul aria-label={t("role.itsPermissions")} className="flex flex-wrap gap-1.5">
              {inOrder(catalogue, role.permissions).map((p) => (
                <li key={p}>
                  <Badge tone="outline" className="font-mono">
                    {p}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Panel>
      <div className="flex flex-col gap-2 lg:self-start">
        {role.built_in && (
          <p className="text-secondary text-muted">
            {t("role.builtInNote")} {canMake && t("role.makeYourOwn")}
          </p>
        )}
        <Uses role={role} />
      </div>
    </div>
  );
}

const saveKey = ["admin", "role", "save"];

function SaveButton({ formId, label }: { formId: string; label: string }) {
  const { t } = useTranslation("admin-access");
  const saving = useIsMutating({ mutationKey: saveKey }) > 0;
  return (
    <Button type="submit" form={formId} variant="primary" disabled={saving}>
      {saving ? t("role.saving") : label}
    </Button>
  );
}

function RoleMenu({ role, editable, onDelete }: { role: S["Role"]; editable: boolean; onDelete: () => void }) {
  const { t } = useTranslation("admin-access");
  const navigate = useNavigate();
  return (
    <Menu>
      <MenuTrigger asChild>
        <IconButton label={t("role.moreActions", { name: roleName(role) })} variant="secondary" className="size-8">
          <Ellipsis />
        </IconButton>
      </MenuTrigger>
      <MenuContent>
        <MenuItem onSelect={() => void navigate({ to: "/admin/roles/new", search: { from: role.key } })}>
          {t("roles.makeFrom")}
        </MenuItem>
        {editable && (
          <>
            <MenuSeparator />
            <MenuItem danger disabled={Boolean(cantDelete(role))} onSelect={onDelete}>
              {cantDelete(role) ?? t("role.deleteRole")}
            </MenuItem>
          </>
        )}
      </MenuContent>
    </Menu>
  );
}

export function RolePage({ roleKey }: { roleKey: string }) {
  const { t } = useTranslation("admin-access");
  const { me } = useCurrentProject();
  const navigate = useNavigate();
  const formId = useId();
  const role = useQuery(adm.role(roleKey));
  const catalogue = useQuery(adm.permissions());
  const [deleting, setDeleting] = useState(false);
  const r = role.data;
  const canMake = can(me, "access.manage");
  const editable = Boolean(r && canMake && !r.built_in);
  return (
    <Page>
      <PageHeader
        breadcrumbs={<Crumbs />}
        title={r ? roleName(r) : t("role.fallback")}
        meta={
          r && (
            <>
              <span className="font-mono">{r.key}</span>
              <Badge tone={r.built_in ? "neutral" : "accent"}>
                {r.built_in ? t("roles.builtIn") : t("roles.custom")}
              </Badge>
              <span>{scopeText(r.scope)}</span>
            </>
          )
        }
        actions={
          r &&
          canMake &&
          (editable ? (
            <>
              <RoleMenu role={r} editable onDelete={() => setDeleting(true)} />
              <SaveButton formId={formId} label={t("role.save")} />
            </>
          ) : (
            <Button asChild variant="primary">
              <Link to="/admin/roles/new" search={{ from: r.key }}>
                {t("roles.makeFrom")}
              </Link>
            </Button>
          ))
        }
      />
      {(role.isPending || catalogue.isPending) && !role.isError && !catalogue.isError && <Skeleton className="h-64" />}
      {role.isError && <ErrorState error={role.error} onRetry={() => void role.refetch()} />}
      {catalogue.isError && <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />}
      {r &&
        catalogue.data &&
        (editable ? (
          <RoleForm key={r.key} formId={formId} catalogue={catalogue.data} role={r} />
        ) : (
          <RoleView catalogue={catalogue.data} role={r} canMake={canMake} />
        ))}
      {r && deleting && (
        <DeleteRole role={r} onClose={() => setDeleting(false)} onDeleted={() => navigate({ to: "/admin/roles" })} />
      )}
    </Page>
  );
}

export function NewRolePage({ from }: { from?: string }) {
  const { t } = useTranslation("admin-access");
  const navigate = useNavigate();
  const formId = useId();
  const source = useQuery({ ...adm.role(from ?? ""), enabled: Boolean(from) });
  const catalogue = useQuery(adm.permissions());
  const unknown = source.isError && isApiError(source.error) && source.error.status === 404;
  useEffect(() => {
    if (!unknown) return;
    toast.error(t("role.noSource", { key: from }));
    void navigate({ to: "/admin/roles", replace: true });
  }, [unknown, from, navigate, t]);
  const ready = catalogue.data && (!from || source.data);
  return (
    <Page>
      <PageHeader
        breadcrumbs={<Crumbs />}
        title={t("role.newTitle")}
        description={source.data ? t("role.startingFrom", { name: roleName(source.data) }) : t("role.choose")}
        actions={ready && <SaveButton formId={formId} label={t("role.make")} />}
      />
      {!ready && !catalogue.isError && !source.isError && <Skeleton className="h-64" />}
      {catalogue.isError && <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />}
      {source.isError && !unknown && <ErrorState error={source.error} onRetry={() => void source.refetch()} />}
      {ready && <RoleForm formId={formId} catalogue={catalogue.data} start={source.data?.permissions} />}
    </Page>
  );
}
