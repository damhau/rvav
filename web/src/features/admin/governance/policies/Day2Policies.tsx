import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Wrench } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, priorityOf, writable } from "../common";
import { LevelsEditor, list, OrderedMenu, PriorityDialog, useOrderedPolicies, useProjects } from "./shared";

type S = components["schemas"];
type Policy = S["Day2Policy"];

const effectTone = { allow: "ok", deny: "bad", approval: "warn" } as const;
const effectLabel = { allow: "Allow", deny: "Deny", approval: "Needs approval" } as const;

const put = ({ id, ...body }: Policy) => call(api.PUT("/admin/day2-policies/{id}", { params: { path: { id } }, body }));

function Day2Sheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
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
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={policy ? `Change ${policy.name}` : "New day-2 policy"}
      description="The first policy that matches an action decides whether it runs."
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save policy"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label="Name" htmlFor="d2-name" error={errors["/name"]}>
          <Input id="d2-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label="Priority" htmlFor="d2-priority" hint="Lower first">
          <Input id="d2-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
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
      <Field
        label="Actions"
        htmlFor="d2-actions"
        error={errorAt(errors, "/actions")}
        hint="Comma-separated, * for all, for example snapshot_*, delete"
      >
        <Input
          id="d2-actions"
          className="font-mono text-secondary"
          value={actions}
          onChange={(e) => setActions(e.target.value)}
        />
      </Field>
      <Field
        label="Roles"
        htmlFor="d2-roles"
        error={errorAt(errors, "/roles")}
        hint="owner, member, project-admin, admin; empty for anyone"
      >
        <Input
          id="d2-roles"
          className="font-mono text-secondary"
          value={roles}
          onChange={(e) => setRoles(e.target.value)}
        />
      </Field>
      <Field
        label="Applies when (CEL)"
        htmlFor="d2-condition"
        error={errors["/condition"]}
        hint="For example environment.production"
      >
        <Textarea
          id="d2-condition"
          className="font-mono text-secondary"
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
      <label htmlFor="d2-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="d2-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
        Enabled
      </label>
    </Sheet>
  );
}

export function Day2Policies({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.day2());
  const projects = useProjects();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [ranking, setRanking] = useState<Policy | null>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const ops = useOrderedPolicies<Policy>("day2-policies", put);
  const remove = useMutation({
    mutationFn: (p: Policy) => call(api.DELETE("/admin/day2-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void qc.invalidateQueries({ queryKey: ["admin", "day2-policies"] });
      toast.success(`${p.name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const items = policies.data ? [...policies.data.items].sort((a, b) => a.priority - b.priority) : undefined;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New day-2 policy
    </Button>
  );
  const columns: Column<Policy>[] = [
    {
      id: "policy",
      header: "Policy",
      cell: (p) => (
        <>
          <span className="font-medium">{p.name}</span>
          {!p.enabled && (
            <Badge tone="neutral" className="ml-2">
              Off
            </Badge>
          )}
          <div className="text-secondary text-muted">
            {p.project_id ? projects.name(p.project_id) : "Every project"}
          </div>
        </>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      cell: (p) => <span className="font-mono text-secondary">{(p.actions ?? []).join(", ")}</span>,
    },
    {
      id: "who",
      header: "Who and when",
      cell: (p) => (
        <span className="text-secondary">
          {(p.roles ?? []).length ? (p.roles ?? []).join(", ") : "Anyone"}
          {p.condition !== "true" && <span className="block font-mono text-muted">{p.condition}</span>}
        </span>
      ),
    },
    { id: "effect", header: "Effect", cell: (p) => <Badge tone={effectTone[p.effect]}>{effectLabel[p.effect]}</Badge> },
    { id: "priority", header: "Priority", align: "end", cell: (p) => <span className="tnum">{p.priority}</span> },
  ];
  return (
    <Panel
      title="Day-2 policies"
      description="The first policy that matches an action decides; when none does, the action is denied."
      flush
      actions={canEdit && Boolean(items?.length) && add}
    >
      <DataTable
        label="Day-2 policies"
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={760}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Wrench}
            title="No day-2 policy"
            body="Every day-2 action is denied until a policy allows it."
            action={canEdit && add}
          />
        }
        rowMenuLabel={(p) => `Actions for ${p.name}`}
        rowMenu={
          canEdit && items
            ? (p) => (
                <OrderedMenu
                  policy={p}
                  items={items}
                  index={items.indexOf(p)}
                  ops={ops}
                  onChange={() => setEditing(p)}
                  onPriority={() => setRanking(p)}
                  onRemove={() => setRemoving(p)}
                />
              )
            : undefined
        }
      />
      {editing && <Day2Sheet policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {ranking && <PriorityDialog policy={ranking} ops={ops} onClose={() => setRanking(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description={
            <>
              It stops deciding <span className="font-mono">{(removing.actions ?? []).join(", ")}</span>.
            </>
          }
          consequences="Those actions are decided by the next policy that matches them, and denied when none does."
          confirmLabel="Remove policy"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
