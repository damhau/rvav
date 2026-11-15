// One role (ADR-0034): what it allows, in the catalogue's words, and a
// custom one's editor for those holding access.manage.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors } from "../governance/common";
import { allows, byArea, type Catalogue, inOrder, scopeOf, scopeText } from "./permissions";
import { AdminWarning, Refusal, refused } from "./refusal";

type S = components["schemas"];

const crumbs = (
  <Link to="/admin/roles" className="hover:text-fg">
    Roles
  </Link>
);

const givenText = (scope: S["RoleScope"], why?: string) =>
  scope === "project"
    ? "Given in a project only."
    : scope === "rvav"
      ? `Given for all of rvav only${why ? `: ${why}` : "."}`
      : "Given in a project, or for all of rvav to allow the same in every project.";

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
  const sentences = allows(catalogue, permissions);
  const { scope, why } = builtInScope ? { scope: builtInScope, why: undefined } : scopeOf(catalogue, permissions);
  return (
    <div className="flex flex-col gap-3 text-[13px]">
      {permissions.includes("access.manage") && <AdminWarning />}
      {sentences.length === 0 ? (
        <p className="text-xs text-muted">Nothing on its own.</p>
      ) : (
        <ul className="flex list-disc flex-col gap-1 pl-4">
          {sentences.map((s) => (
            <li key={s.id}>{s.text}</li>
          ))}
        </ul>
      )}
      {sentences.length > 0 && <p className="text-xs text-muted">{givenText(scope, why)}</p>}
    </div>
  );
}

function Uses({ role }: { role: S["Role"] }) {
  return (
    <p className="text-xs text-muted">
      {role.holders === 0
        ? "Given to nobody."
        : `Given ${role.holders} ${role.holders === 1 ? "time" : "times"}, to users or groups.`}
      {role.policies.length > 0 && ` Named by the day-2 policies ${role.policies.join(", ")}.`}
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
        <label htmlFor={box} className={mono ? "font-mono text-xs" : "text-[13px]"}>
          {label}
        </label>
        {what && (
          <p id={`${box}-what`} className="text-xs text-muted">
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
  const held = new Set(value);
  const set = (id: string, on: boolean, drop: (p: string) => boolean = () => false) =>
    onChange(on ? [...value.filter((p) => !drop(p)), id] : value.filter((p) => p !== id));
  return (
    <div className="flex flex-col gap-4">
      {byArea(catalogue).map(([area, items]) => (
        <fieldset key={area} className="flex flex-col">
          <legend className="label mb-1">{area}</legend>
          {items.map((p) => (
            <PermissionBox
              key={p.id}
              id={p.id}
              label={p.id}
              mono
              what={
                <>
                  {p.summary}
                  {p.scope === "rvav" && (
                    <Badge tone="outline" className="ml-1.5">
                      All of rvav only
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
        <legend className="label mb-1">Day-2 actions</legend>
        <p className="text-xs text-muted">
          A group allows each of its actions, those added later included; or choose single actions.
        </p>
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
                what={`Every ${g.group} action.`}
                checked={all}
                onChange={(on) => set(g.permission, on, (p) => p.startsWith(prefix))}
              />
              {actions.length > 0 && (
                <div className="ml-6 grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                  {actions.map((a) => (
                    <PermissionBox
                      key={a.permission}
                      id={a.permission}
                      label={a.name}
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

/** A custom role's editor, or a new role's. */
function RoleForm({ catalogue, role, start }: { catalogue: Catalogue; role?: S["Role"]; start?: string[] }) {
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
    mutationFn: () => {
      const body = { name: f.name, description: f.description, permissions: inOrder(catalogue, f.permissions) };
      return role
        ? call(api.PATCH("/admin/roles/{key}", { params: { path: { key: role.key } }, body }))
        : call(api.POST("/admin/roles", { body: { ...body, key: f.key } }));
    },
    onSuccess: (r) => {
      changed();
      toast.success(role ? `${r.name} saved` : `${r.name} made`);
      if (!role) void navigate({ to: "/admin/roles/$roleKey", params: { roleKey: r.key } });
    },
    onError: (e) => {
      if (!refused(e, [409, 422])) failed(e, "Not saved");
    },
  });
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/roles/{key}", { params: { path: { key: role!.key } } })),
    onSuccess: () => {
      changed();
      toast.success(`${role!.name} deleted`);
      void navigate({ to: "/admin/roles" });
    },
    onError: (e) => {
      if (!refused(e, [409])) failed(e, "Not deleted");
    },
  });
  const errors = fieldErrors(save.error);
  const keyTaken = !role && isApiError(save.error) && save.error.status === 409 ? save.error.problem.detail : undefined;
  const permissionsError = errorAt(errors, "/permissions");
  return (
    <form
      className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_340px]"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex min-w-0 flex-col gap-5">
        <Panel title="Role">
          <div className="grid max-w-2xl grid-cols-1 gap-3 md:grid-cols-2">
            <Field
              label="Key"
              htmlFor="role-key"
              error={errors["/key"] ?? keyTaken}
              hint={
                role
                  ? "Fixed once made."
                  : "Fixed once made: 2 to 41 lower-case letters, digits and hyphens, starting with a letter."
              }
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
            <Field label="Name" htmlFor="role-name" error={errors["/name"]}>
              <Input id="role-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
            </Field>
            <Field
              label="Description"
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
        <Panel title="Permissions">
          {permissionsError && (
            <p role="alert" className="mb-3 flex items-start gap-1.5 text-xs text-bad">
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
        <Panel title="What it allows">
          <Allows catalogue={catalogue} permissions={f.permissions} />
        </Panel>
        <div className="flex flex-col gap-2">
          {role && <Uses role={role} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="primary" disabled={save.isPending}>
              {role ? "Save role" : "Make role"}
            </Button>
            {role && (
              <Button
                type="button"
                variant="danger"
                disabled={remove.isPending}
                onClick={() =>
                  window.confirm(`Delete ${role.name}? Nobody can be given it any more.`) && remove.mutate()
                }
              >
                Delete role
              </Button>
            )}
          </div>
          {!keyTaken && <Refusal error={save.error} only={[409]} />}
          <Refusal error={remove.error} only={[409]} />
        </div>
      </div>
    </form>
  );
}

/** A role that can't be changed here: built in, or read by someone without access.manage. */
function RoleView({ catalogue, role, canMake }: { catalogue: Catalogue; role: S["Role"]; canMake: boolean }) {
  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
      <Panel title="What it allows">
        <div className="flex flex-col gap-3">
          {role.description && <p className="text-[13px]">{role.description}</p>}
          <Allows
            catalogue={catalogue}
            permissions={role.permissions}
            builtInScope={role.built_in ? role.scope : undefined}
          />
          {role.permissions.length > 0 && (
            <ul aria-label="Its permissions" className="flex flex-wrap gap-1.5">
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
          <p className="text-xs text-muted">
            Built in: it can&apos;t be changed or deleted.{" "}
            {canMake && "To give someone a little more or less, make a role of your own from it."}
          </p>
        )}
        <Uses role={role} />
        {canMake && (
          <div>
            <Button asChild size="sm">
              <Link to="/admin/roles/new" search={{ from: role.key }}>
                Make a role from it
              </Link>
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

export function RolePage({ roleKey }: { roleKey: string }) {
  const { me } = useCurrentProject();
  const role = useQuery(adm.role(roleKey));
  const catalogue = useQuery(adm.permissions());
  const r = role.data;
  return (
    <Page>
      <PageHeader
        crumbs={crumbs}
        title={r?.name ?? "Role"}
        meta={
          r && (
            <>
              <span className="font-mono">{r.key}</span>
              <Badge tone={r.built_in ? "neutral" : "accent"}>{r.built_in ? "Built in" : "Custom"}</Badge>
              <span>{scopeText[r.scope]}</span>
            </>
          )
        }
      />
      {(role.isPending || catalogue.isPending) && !role.isError && !catalogue.isError && <Skeleton className="h-64" />}
      {role.isError && <ErrorState error={role.error} onRetry={() => void role.refetch()} />}
      {catalogue.isError && <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />}
      {r &&
        catalogue.data &&
        (can(me, "access.manage") && !r.built_in ? (
          <RoleForm key={r.key} catalogue={catalogue.data} role={r} />
        ) : (
          <RoleView catalogue={catalogue.data} role={r} canMake={can(me, "access.manage")} />
        ))}
    </Page>
  );
}

export function NewRolePage({ from }: { from?: string }) {
  const source = useQuery({ ...adm.role(from ?? ""), enabled: Boolean(from) });
  const catalogue = useQuery(adm.permissions());
  const ready = catalogue.data && (!from || source.data || source.isError);
  return (
    <Page>
      <PageHeader
        crumbs={crumbs}
        title="New role"
        meta={source.data ? `Starting from what ${source.data.name} allows.` : "Choose what it allows."}
      />
      {!ready && !catalogue.isError && <Skeleton className="h-64" />}
      {catalogue.isError && <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />}
      {ready && <RoleForm catalogue={catalogue.data} start={source.data?.permissions} />}
    </Page>
  );
}
