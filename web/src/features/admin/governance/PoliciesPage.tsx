// Policies (S9, ADR-0020): approval, day-2, lease and naming policies, and
// what applies for a project, environment, item or action. Conditions are CEL,
// checked by the server on sample variables when saved.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm, q } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import {
  errorAt,
  failed,
  fieldErrors,
  LevelsEditor,
  OfferEditor,
  offersText,
  priorityOf,
  td,
  th,
  writable,
} from "./common";

type S = components["schemas"];

const day2Actions = [
  "power_on", "power_off", "shutdown_guest", "restart_guest", "reset", "console", "resize", "disk_add", "disk_extend",
  "nic_add", "nic_move", "nic_remove", "iso_mount", "iso_unmount", "snapshot_create", "snapshot_revert", "snapshot_delete",
  "rename", "change_owner", "change_project", "extend_lease", "delete",
]; // prettier-ignore

const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

function useProjects() {
  const projects = useQuery(adm.projects("", false));
  const items = projects.data?.items ?? [];
  return {
    items,
    name: (id: string) => items.find((p) => p.id === id)?.name ?? id,
    options: (all: string) => [{ value: "all", label: all }, ...items.map((p) => ({ value: p.id, label: p.name }))],
  };
}

/**
 * Moves a policy up or down: the list in its new order is numbered 10, 20,
 * 30…, so ties can't keep a policy in place, and only the policies whose
 * priority changes are saved.
 */
function useReorder<T extends { id: string; priority: number }>(
  put: (p: T, priority: number) => Promise<unknown>,
  key: string,
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ items, from, to }: { items: T[]; from: number; to: number }) => {
      const order = [...items];
      const [moved] = order.splice(from, 1);
      order.splice(to, 0, moved!);
      for (const [i, p] of order.entries()) {
        if (p.priority !== (i + 1) * 10) await put(p, (i + 1) * 10);
      }
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: ["admin", key] }),
    onError: (e) => failed(e, "Not moved"),
  });
}

function Order({
  name,
  i,
  count,
  busy,
  move,
}: {
  name: string;
  i: number;
  count: number;
  busy: boolean;
  move: (to: number) => void;
}) {
  return (
    <span className="inline-flex">
      <Button
        size="icon"
        variant="ghost"
        aria-label={`Move ${name} up`}
        disabled={busy || i === 0}
        onClick={() => move(i - 1)}
      >
        <ArrowUp className="size-3.5" />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        aria-label={`Move ${name} down`}
        disabled={busy || i === count - 1}
        onClick={() => move(i + 1)}
      >
        <ArrowDown className="size-3.5" />
      </Button>
    </span>
  );
}

// ── Approval policies ──

function ApprovalDialog({ policy, onClose }: { policy?: S["ApprovalPolicy"]; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useProjects();
  const [f, setF] = useState<S["ApprovalPolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : {
          name: "",
          reason: "",
          condition: "true",
          item_type: null,
          project_id: null,
          levels: [{ name: "Level 1", mode: "any" }],
          priority: 100,
          enabled: true,
          auto_approve: null,
        },
  );
  const [priority, setPriority] = useState(String(f.priority));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      policy
        ? call(
            api.PUT("/admin/approval-policies/{id}", {
              params: { path: { id: policy.id } },
              body: { ...f, priority: priorityOf(priority) },
            }),
          )
        : call(api.POST("/admin/approval-policies", { body: { ...f, priority: priorityOf(priority) } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "approval-policies"] });
      toast.success(`${f.name} saved`);
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
        title={policy ? `Change ${policy.name}` : "New approval policy"}
        className="w-[min(760px,calc(100vw-32px))]"
      >
        <form
          className="flex max-h-[75vh] flex-col gap-3 overflow-y-auto p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_120px]">
            <Field label="Name" htmlFor="ap-name" error={errors["/name"]}>
              <Input id="ap-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
            </Field>
            <Field label="Priority" htmlFor="ap-priority" hint="Lower first">
              <Input id="ap-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
            </Field>
          </div>
          <Field label="Reason requesters see" htmlFor="ap-reason">
            <Input id="ap-reason" value={f.reason ?? ""} onChange={(e) => setF({ ...f, reason: e.target.value })} />
          </Field>
          <Field
            label="Applies when (CEL)"
            htmlFor="ap-condition"
            error={errors["/condition"]}
            hint={'For example environment == "prd" || inputs.cpu_count > 8'}
          >
            <Textarea
              id="ap-condition"
              className="font-mono text-xs"
              rows={2}
              value={f.condition}
              onChange={(e) => setF({ ...f, condition: e.target.value })}
            />
          </Field>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field label="Kind of item" htmlFor="ap-type">
              <Select
                id="ap-type"
                value={f.item_type ?? "all"}
                onChange={(v) => setF({ ...f, item_type: v === "all" ? null : (v as S["CatalogItemType"]) })}
                options={[
                  { value: "all", label: "Any" },
                  { value: "vm_blueprint", label: "VM blueprints" },
                  { value: "awx_job_template", label: "AWX job templates" },
                  { value: "awx_workflow", label: "AWX workflows" },
                  { value: "composite", label: "Composite" },
                ]}
              />
            </Field>
            <Field label="Project" htmlFor="ap-project">
              <Select
                id="ap-project"
                value={f.project_id ?? "all"}
                onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
                options={projects.options("Every project")}
              />
            </Field>
          </div>
          <Field label="Approved at once when (CEL, optional)" htmlFor="ap-auto" error={errors["/auto_approve"]}>
            <Input
              id="ap-auto"
              className="font-mono text-xs"
              value={f.auto_approve ?? ""}
              onChange={(e) => setF({ ...f, auto_approve: e.target.value || null })}
            />
          </Field>
          <LevelsEditor value={f.levels} onChange={(levels) => setF({ ...f, levels })} errors={errors} />
          <label htmlFor="ap-enabled" className="flex items-center gap-1.5 text-[13px]">
            <Checkbox id="ap-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
            Enabled
          </label>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save policy
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Approvals({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.approvals());
  const projects = useProjects();
  const [editing, setEditing] = useState<S["ApprovalPolicy"] | "new" | null>(null);
  const reorder = useReorder<S["ApprovalPolicy"]>(
    ({ id, ...body }, priority) =>
      call(api.PUT("/admin/approval-policies/{id}", { params: { path: { id } }, body: { ...body, priority } })),
    "approval-policies",
  );
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/approval-policies/{id}", { params: { path: { id } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "approval-policies"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  const items = [...(policies.data?.items ?? [])].sort((a, b) => a.priority - b.priority);
  return (
    <Panel
      title="Approval policies"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New approval policy
          </Button>
        )
      }
    >
      {policies.isPending && <SkeletonRows rows={3} cols={4} />}
      {policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
      {policies.data && items.length === 0 && (
        <EmptyState title="No approval policy" body="Every request runs at once." />
      )}
      {items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Policy
                </th>
                <th scope="col" className={th}>
                  Applies when
                </th>
                <th scope="col" className={th}>
                  Approvers
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((p, i) => (
                <tr key={p.id}>
                  <td className={`${td} pl-4`}>
                    <span className="font-medium">{p.name}</span>
                    {!p.enabled && (
                      <Badge tone="neutral" className="ml-2">
                        Off
                      </Badge>
                    )}
                    <div className="text-xs text-faint">
                      {p.project_id ? projects.name(p.project_id) : "Every project"} · priority {p.priority}
                    </div>
                  </td>
                  <td className={`${td} font-mono text-xs`}>{p.condition}</td>
                  <td className={`${td} text-xs text-muted`}>
                    {p.levels.map((l) => l.group ?? l.approvers?.join(", ") ?? l.name).join(" → ")}
                  </td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {canEdit && (
                      <>
                        <Order
                          name={p.name}
                          i={i}
                          count={items.length}
                          busy={reorder.isPending}
                          move={(to) => reorder.mutate({ items, from: i, to })}
                        />
                        <Button size="sm" variant="ghost" onClick={() => setEditing(p)}>
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${p.name}`}
                          onClick={() => window.confirm(`Remove ${p.name}?`) && remove.mutate(p.id)}
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
      {editing && <ApprovalDialog policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

// ── Day-2 policies ──

function Day2Dialog({ policy, onClose }: { policy?: S["Day2Policy"]; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useProjects();
  const [f, setF] = useState<S["Day2PolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : {
          name: "",
          reason: "",
          priority: 100,
          actions: ["*"],
          roles: [],
          condition: "true",
          effect: "allow",
          levels: [],
          project_id: null,
          enabled: true,
        },
  );
  const [priority, setPriority] = useState(String(f.priority));
  const [actions, setActions] = useState((f.actions ?? []).join(", "));
  const [roles, setRoles] = useState((f.roles ?? []).join(", "));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...f,
        actions: list(actions),
        roles: list(roles),
        levels: f.effect === "approval" ? f.levels : [],
        priority: priorityOf(priority),
      };
      return policy
        ? call(api.PUT("/admin/day2-policies/{id}", { params: { path: { id: policy.id } }, body }))
        : call(api.POST("/admin/day2-policies", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "day2-policies"] });
      toast.success(`${f.name} saved`);
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
        title={policy ? `Change ${policy.name}` : "New day-2 policy"}
        className="w-[min(760px,calc(100vw-32px))]"
      >
        <form
          className="flex max-h-[75vh] flex-col gap-3 overflow-y-auto p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_120px_160px]">
            <Field label="Name" htmlFor="d2-name" error={errors["/name"]}>
              <Input id="d2-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
            </Field>
            <Field label="Priority" htmlFor="d2-priority" hint="Lower first">
              <Input id="d2-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
            </Field>
            <Field label="Effect" htmlFor="d2-effect">
              <Select
                id="d2-effect"
                value={f.effect}
                onChange={(v) =>
                  setF({
                    ...f,
                    effect: v as S["Day2PolicyWrite"]["effect"],
                    levels: v === "approval" && !f.levels?.length ? [{ name: "Level 1", mode: "any" }] : f.levels,
                  })
                }
                options={[
                  { value: "allow", label: "Allow" },
                  { value: "deny", label: "Deny" },
                  { value: "approval", label: "Needs approval" },
                ]}
              />
            </Field>
          </div>
          <Field
            label="Actions"
            htmlFor="d2-actions"
            error={errorAt(errors, "/actions")}
            hint={"Comma-separated, * for all, for example snapshot_*, delete"}
          >
            <Input
              id="d2-actions"
              className="font-mono text-xs"
              value={actions}
              onChange={(e) => setActions(e.target.value)}
            />
          </Field>
          <Field
            label="Roles"
            htmlFor="d2-roles"
            error={errorAt(errors, "/roles")}
            hint={"owner, member, project-admin, admin; empty for anyone"}
          >
            <Input
              id="d2-roles"
              className="font-mono text-xs"
              value={roles}
              onChange={(e) => setRoles(e.target.value)}
            />
          </Field>
          <Field
            label="Applies when (CEL)"
            htmlFor="d2-condition"
            error={errors["/condition"]}
            hint={"For example environment.production"}
          >
            <Textarea
              id="d2-condition"
              className="font-mono text-xs"
              rows={2}
              value={f.condition}
              onChange={(e) => setF({ ...f, condition: e.target.value })}
            />
          </Field>
          <Field label="Reason people see" htmlFor="d2-reason">
            <Input id="d2-reason" value={f.reason ?? ""} onChange={(e) => setF({ ...f, reason: e.target.value })} />
          </Field>
          <Field label="Project" htmlFor="d2-project">
            <Select
              id="d2-project"
              value={f.project_id ?? "all"}
              onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
              options={projects.options("Every project")}
            />
          </Field>
          {f.effect === "approval" && (
            <LevelsEditor value={f.levels ?? []} onChange={(levels) => setF({ ...f, levels })} errors={errors} />
          )}
          <label htmlFor="d2-enabled" className="flex items-center gap-1.5 text-[13px]">
            <Checkbox id="d2-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
            Enabled
          </label>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save policy
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const effectTone = { allow: "ok", deny: "bad", approval: "warn" } as const;

function Day2({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.day2());
  const [editing, setEditing] = useState<S["Day2Policy"] | "new" | null>(null);
  const reorder = useReorder<S["Day2Policy"]>(
    ({ id, ...body }, priority) =>
      call(api.PUT("/admin/day2-policies/{id}", { params: { path: { id } }, body: { ...body, priority } })),
    "day2-policies",
  );
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/day2-policies/{id}", { params: { path: { id } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "day2-policies"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  const items = [...(policies.data?.items ?? [])].sort((a, b) => a.priority - b.priority);
  return (
    <Panel
      title="Day-2 policies"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New day-2 policy
          </Button>
        )
      }
    >
      <p className="border-b border-line px-4 py-2 text-xs text-muted">
        The first policy that matches an action decides; when none does, the action is denied.
      </p>
      {policies.isPending && <SkeletonRows rows={3} cols={4} />}
      {policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
      {items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Policy
                </th>
                <th scope="col" className={th}>
                  Actions
                </th>
                <th scope="col" className={th}>
                  Who and when
                </th>
                <th scope="col" className={th}>
                  Effect
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((p, i) => (
                <tr key={p.id}>
                  <td className={`${td} pl-4`}>
                    <span className="font-medium">{p.name}</span>
                    {!p.enabled && (
                      <Badge tone="neutral" className="ml-2">
                        Off
                      </Badge>
                    )}
                    <div className="text-xs text-faint">priority {p.priority}</div>
                  </td>
                  <td className={`${td} font-mono text-xs`}>{(p.actions ?? []).join(", ")}</td>
                  <td className={`${td} text-xs`}>
                    {(p.roles ?? []).length ? (p.roles ?? []).join(", ") : "Anyone"}
                    {p.condition !== "true" && <div className="font-mono text-faint">{p.condition}</div>}
                  </td>
                  <td className={td}>
                    <Badge tone={effectTone[p.effect]}>
                      {p.effect === "approval" ? "Needs approval" : p.effect === "allow" ? "Allow" : "Deny"}
                    </Badge>
                  </td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {canEdit && (
                      <>
                        <Order
                          name={p.name}
                          i={i}
                          count={items.length}
                          busy={reorder.isPending}
                          move={(to) => reorder.mutate({ items, from: i, to })}
                        />
                        <Button size="sm" variant="ghost" onClick={() => setEditing(p)}>
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${p.name}`}
                          onClick={() => window.confirm(`Remove ${p.name}?`) && remove.mutate(p.id)}
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
      {editing && <Day2Dialog policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

// ── Lease policies ──

const expiryLabels: Record<S["LeaseExpiryAction"], string> = {
  power_off_then_delete: "Power off, then delete after the grace period",
  delete: "Delete after the grace period",
  notify_only: "Only tell the owner",
};

function LeaseDialog({ lease, onClose }: { lease?: S["LeasePolicy"]; onClose: () => void }) {
  const qc = useQueryClient();
  const envs = useQuery(q.environments());
  const projects = useProjects();
  const [f, setF] = useState<S["LeasePolicy"]>(
    lease ?? {
      environment: "dev",
      project_id: null,
      default_days: 30,
      max_days: 90,
      max_extensions: 2,
      grace_days: 7,
      expiry_action: "power_off_then_delete",
    },
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => call(api.PUT("/admin/lease-policies", { body: f })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "leases"] });
      toast.success("Lease policy saved");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const days = (k: "default_days" | "max_days", label: string) => (
    <Field label={label} htmlFor={`lease-${k}`} error={errors[`/${k}`]} hint={"Empty for none"}>
      <Input
        id={`lease-${k}`}
        type="number"
        min={1}
        value={f[k] ?? ""}
        onChange={(e) => setF({ ...f, [k]: e.target.value === "" ? null : Number(e.target.value) })}
      />
    </Field>
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={lease ? "Change the lease policy" : "New lease policy"}>
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <div className="grid grid-cols-2 gap-3">
            <Field label="Environment" htmlFor="lease-env">
              <Select
                id="lease-env"
                value={f.environment}
                disabled={Boolean(lease)}
                onChange={(v) => setF({ ...f, environment: v })}
                options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
              />
            </Field>
            <Field label="Project" htmlFor="lease-project" hint="A project's own policy wins">
              <Select
                id="lease-project"
                value={f.project_id ?? "all"}
                disabled={Boolean(lease)}
                onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
                options={projects.options("Every project")}
              />
            </Field>
            {days("default_days", "Lease, in days")}
            {days("max_days", "At most, in days")}
            <Field label="Extensions" htmlFor="lease-ext" error={errors["/max_extensions"]}>
              <Input
                id="lease-ext"
                type="number"
                min={0}
                value={f.max_extensions}
                onChange={(e) => setF({ ...f, max_extensions: Number(e.target.value) })}
              />
            </Field>
            <Field label="Grace, in days" htmlFor="lease-grace" error={errors["/grace_days"]}>
              <Input
                id="lease-grace"
                type="number"
                min={0}
                value={f.grace_days}
                onChange={(e) => setF({ ...f, grace_days: Number(e.target.value) })}
              />
            </Field>
          </div>
          <Field label="When it expires" htmlFor="lease-action">
            <Select
              id="lease-action"
              value={f.expiry_action}
              onChange={(v) => setF({ ...f, expiry_action: v as S["LeaseExpiryAction"] })}
              options={Object.entries(expiryLabels).map(([value, label]) => ({ value, label }))}
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save lease policy
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Leases({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const leases = useQuery(adm.leases());
  const projects = useProjects();
  const [editing, setEditing] = useState<S["LeasePolicy"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: ({ environment, project_id }: { environment: string; project_id: string }) =>
      call(api.DELETE("/admin/lease-policies", { params: { query: { environment, project_id } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "leases"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="Lease policies"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New lease policy
          </Button>
        )
      }
    >
      {leases.isPending && <SkeletonRows rows={3} cols={4} />}
      {leases.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Where
                </th>
                <th scope="col" className={th}>
                  Lease
                </th>
                <th scope="col" className={th}>
                  Extensions
                </th>
                <th scope="col" className={th}>
                  When it expires
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {leases.data.items.map((l) => {
                const where = `${l.environment}${l.project_id ? `, ${projects.name(l.project_id)}` : ""}`;
                return (
                  <tr key={`${l.environment}-${l.project_id ?? ""}`}>
                    <td className={`${td} pl-4`}>
                      {l.environment}
                      {l.project_id && <span className="text-xs text-muted"> · {projects.name(l.project_id)}</span>}
                    </td>
                    <td className={`${td} text-xs`}>
                      {l.default_days ? `${l.default_days} days, up to ${l.max_days ?? "—"}` : "None"}
                    </td>
                    <td className={`${td} text-xs`}>{l.max_extensions}</td>
                    <td className={`${td} text-xs text-muted`}>
                      {expiryLabels[l.expiry_action]}, {l.grace_days} days' grace
                    </td>
                    <td className={`${td} text-right whitespace-nowrap`}>
                      {canEdit && (
                        <>
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Change the lease in ${where}`}
                            onClick={() => setEditing(l)}
                          >
                            Change
                          </Button>
                          {l.project_id && (
                            <Button
                              size="sm"
                              variant="ghost"
                              aria-label={`Remove the lease in ${where}`}
                              onClick={() =>
                                remove.mutate({ environment: l.environment, project_id: l.project_id ?? "" })
                              }
                            >
                              Remove
                            </Button>
                          )}
                        </>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {editing && <LeaseDialog lease={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

// ── Naming policies ──

function NamingDialog({ policy, onClose }: { policy?: S["NamingPolicy"]; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useProjects();
  const [f, setF] = useState<S["NamingPolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : { name: "", template: "{project}-{environment}-{role}{seq:03}", counter: "prefix", offers: [] },
  );
  const [sample, setSample] = useState({ project: "", environment: "dev", role: "app" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const preview = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/naming-preview", {
          body: {
            template: f.template,
            counter: f.counter,
            policy_id: policy?.id,
            project_id: sample.project || projects.items[0]?.id || "",
            environment: sample.environment,
            inputs: { role: sample.role },
            count: 3,
          },
        }),
      ),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "No preview");
    },
  });
  const save = useMutation({
    mutationFn: () =>
      policy
        ? call(api.PUT("/admin/naming-policies/{id}", { params: { path: { id: policy.id } }, body: f }))
        : call(api.POST("/admin/naming-policies", { body: f })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "naming"] });
      toast.success(`${f.name} saved`);
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
        title={policy ? `Change ${policy.name}` : "New naming policy"}
        className="w-[min(680px,calc(100vw-32px))]"
      >
        <form
          className="flex max-h-[75vh] flex-col gap-3 overflow-y-auto p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <Field label="Name" htmlFor="nm-name" error={errors["/name"]}>
            <Input id="nm-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field
            label="Template"
            htmlFor="nm-template"
            error={errors["/template"]}
            hint={"{project}, {environment}, {role} or {inputs.x}, ending with {seq:03}"}
          >
            <Input
              id="nm-template"
              className="font-mono text-xs"
              value={f.template}
              onChange={(e) => setF({ ...f, template: e.target.value })}
            />
          </Field>
          <Field label="Counter" htmlFor="nm-counter">
            <Select
              id="nm-counter"
              value={f.counter}
              onChange={(v) => setF({ ...f, counter: v as S["NamingPolicy"]["counter"] })}
              options={[
                { value: "prefix", label: "One per name prefix (web-prd-app, web-prd-db…)" },
                { value: "policy", label: "One for the whole policy" },
              ]}
            />
          </Field>
          <OfferEditor
            value={f.offers ?? []}
            onChange={(offers) => setF({ ...f, offers })}
            error={errorAt(errors, "/offers")}
          />
          <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
            <legend className="px-1 text-xs font-medium">Next names</legend>
            <div className="grid grid-cols-3 gap-2">
              <Select
                aria-label="Preview project"
                value={sample.project || projects.items[0]?.id || ""}
                onChange={(v) => setSample({ ...sample, project: v })}
                options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
              />
              <Input
                aria-label="Preview environment"
                value={sample.environment}
                onChange={(e) => setSample({ ...sample, environment: e.target.value })}
              />
              <Input
                aria-label="Preview role"
                value={sample.role}
                onChange={(e) => setSample({ ...sample, role: e.target.value })}
              />
            </div>
            <div>
              <Button type="button" size="sm" disabled={preview.isPending} onClick={() => preview.mutate()}>
                Preview names
              </Button>
            </div>
            {preview.data && (
              <div className="text-xs" aria-live="polite">
                {preview.data.problem && <p className="text-warn">{preview.data.problem}</p>}
                {preview.data.names.length > 0 && (
                  <p>
                    Next: <span className="font-mono">{preview.data.names.join(", ")}</span>
                  </p>
                )}
                {preview.data.skipped.map((s) => (
                  <p key={s.name} className="text-faint">
                    Skips <span className="font-mono">{s.name}</span>: taken in {s.taken_in}
                  </p>
                ))}
              </div>
            )}
          </fieldset>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save naming policy
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Naming({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.naming());
  const projects = useProjects();
  const [editing, setEditing] = useState<S["NamingPolicy"] | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/naming-policies/{id}", { params: { path: { id } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "naming"] }),
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="Naming policies"
      flush
      actions={
        canEdit && (
          <Button size="sm" onClick={() => setEditing("new")}>
            New naming policy
          </Button>
        )
      }
    >
      <p className="border-b border-line px-4 py-2 text-xs text-muted">
        The most specific offer wins over a blueprint's own template. A name is never given twice: rvav checks vCenter,
        DNS and every deployment it made.
      </p>
      {policies.isPending && <SkeletonRows rows={2} cols={3} />}
      {policies.data && policies.data.items.length === 0 && (
        <EmptyState title="No naming policy" body="Each blueprint's own template names its VMs." />
      )}
      {policies.data && policies.data.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Policy
                </th>
                <th scope="col" className={th}>
                  Template
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
              {policies.data.items.map((p) => (
                <tr key={p.id}>
                  <td className={`${td} pl-4 font-medium`}>{p.name}</td>
                  <td className={`${td} font-mono text-xs`}>{p.template}</td>
                  <td className={`${td} text-xs text-muted`}>{offersText(p.offers, projects.name)}</td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {canEdit && (
                      <>
                        <Button size="sm" variant="ghost" onClick={() => setEditing(p)}>
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${p.name}`}
                          onClick={() => remove.mutate(p.id)}
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
      {editing && <NamingDialog policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}

// ── What applies here ──

function Decision({ title, d }: { title: string; d?: S["PolicyDecision"] }) {
  if (!d) return null;
  return (
    <section aria-label={title} className="flex flex-col gap-1 rounded-md border border-line p-3">
      <h3 className="text-xs font-semibold">{title}</h3>
      <p className="text-[13px]">{d.outcome}</p>
      <p className="text-xs text-muted">Decided by: {d.decided_by ?? "nothing"}</p>
      {d.considered.length > 0 && (
        <ul className="mt-1 flex flex-col gap-0.5 text-xs">
          {d.considered.map((c) => (
            <li key={c.id} className={c.applies ? "" : "text-faint"}>
              {c.applies ? "✓" : "–"} {c.name}: {c.why}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Explain() {
  const projects = useProjects();
  const envs = useQuery(q.environments());
  const [f, setF] = useState({ project: "", environment: "", item: "none", action: "none" });
  const project = f.project || projects.items[0]?.id || "";
  // The items the project may request.
  const items = useQuery({ ...q.catalog({ projectId: project }), enabled: Boolean(project) });
  const environment = f.environment || projects.items.find((p) => p.id === project)?.environments[0] || "";
  const explain = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/policies/explain", {
          body: {
            project_id: project,
            environment,
            ...(f.item !== "none" ? { item_id: f.item } : {}),
            ...(f.action !== "none" ? { action: f.action } : {}),
          },
        }),
      ),
    onError: (e) => failed(e, "Couldn't explain"),
  });
  const r = explain.data;
  return (
    <Panel title="What applies here">
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          explain.mutate();
        }}
      >
        <Field label="Project" htmlFor="ex-project" className="w-48">
          <Select
            id="ex-project"
            value={project}
            onChange={(v) => setF({ ...f, project: v, environment: "", item: "none" })}
            options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
          />
        </Field>
        <Field label="Environment" htmlFor="ex-env" className="w-36">
          <Select
            id="ex-env"
            value={environment}
            onChange={(v) => setF({ ...f, environment: v })}
            options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
          />
        </Field>
        <Field label="Catalog item" htmlFor="ex-item" className="w-56">
          <Select
            id="ex-item"
            value={f.item}
            onChange={(v) => setF({ ...f, item: v })}
            options={[
              { value: "none", label: "None" },
              ...(items.data?.items ?? []).map((i) => ({ value: i.id, label: i.name })),
            ]}
          />
        </Field>
        <Field label="Day-2 action" htmlFor="ex-action" className="w-44">
          <Select
            id="ex-action"
            value={f.action}
            onChange={(v) => setF({ ...f, action: v })}
            options={[{ value: "none", label: "None" }, ...day2Actions.map((a) => ({ value: a, label: a }))]}
          />
        </Field>
        <Button type="submit" variant="primary" disabled={!project || !environment || explain.isPending}>
          Explain
        </Button>
      </form>
      {r && (
        <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2" aria-live="polite">
          <Decision title="Approval" d={r.approval} />
          <Decision title="Day-2" d={r.day2} />
          <Decision title="Lease" d={r.lease} />
          <Decision title="Naming" d={r.naming} />
          <section aria-label="Quota" className="flex flex-col gap-1 rounded-md border border-line p-3">
            <h3 className="text-xs font-semibold">Quota</h3>
            {r.quota.length === 0 && <p className="text-xs text-muted">No limit.</p>}
            <ul className="text-xs">
              {r.quota.map((l) => (
                <li key={`${l.resource}-${l.scope}`}>
                  {l.resource} {l.scope ? `in ${l.scope}` : "(project)"}: {l.used + l.reserved} of{" "}
                  {l.limit ?? "no limit"}
                  {l.enforcement === "soft" ? ", then approval" : ""}
                </li>
              ))}
            </ul>
          </section>
        </div>
      )}
    </Panel>
  );
}

export function PoliciesPage() {
  const { me } = useCurrentProject();
  const canEdit = me.global_roles.includes("admin");
  return (
    <Page>
      <PageHeader
        title="Policies"
        meta="Who approves what, which day-2 actions are allowed, how long deployments live and how VMs are named."
      />
      <Tabs defaultValue="approval">
        <TabsList aria-label="Kinds of policy">
          <TabsTrigger value="approval">Approval</TabsTrigger>
          <TabsTrigger value="day2">Day-2</TabsTrigger>
          <TabsTrigger value="lease">Lease</TabsTrigger>
          <TabsTrigger value="naming">Naming</TabsTrigger>
          <TabsTrigger value="explain">What applies here</TabsTrigger>
        </TabsList>
        <TabsContent value="approval" className="pt-4">
          <Approvals canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="day2" className="pt-4">
          <Day2 canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="lease" className="pt-4">
          <Leases canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="naming" className="pt-4">
          <Naming canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="explain" className="pt-4">
          <Explain />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
