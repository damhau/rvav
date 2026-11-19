import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
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
import { failed, fieldErrors, priorityOf, writable } from "../common";
import {
  askedText,
  LevelsEditor,
  movesEveryone,
  OrderedMenu,
  PriorityDialog,
  useOrderedPolicies,
  useProjects,
  type PolicyRights,
} from "./shared";

type S = components["schemas"];
type Policy = S["ApprovalPolicy"];

const itemTypes: { value: S["CatalogItemType"] | "all"; label: string }[] = [
  { value: "all", label: "Any" },
  { value: "vm_blueprint", label: "VM blueprints" },
  { value: "awx_job_template", label: "AWX job templates" },
  { value: "awx_workflow", label: "AWX workflows" },
  { value: "composite", label: "Composite" },
];

const put = ({ id, ...body }: Policy) =>
  call(api.PUT("/admin/approval-policies/{id}", { params: { path: { id } }, body }));

function ApprovalSheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useProjects();
  const listed = useQuery(adm.approvals());
  const [f, setF] = useState<S["ApprovalPolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : {
          name: "",
          reason: "",
          condition: "true",
          item_type: null,
          project_id: projects.initial,
          levels: [{ name: "Level 1", mode: "any" }],
          priority: 100,
          enabled: true,
          auto_approve: null,
        },
  );
  const [priority, setPriority] = useState(String(f.priority));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = { ...f, priority: priorityOf(priority) };
      return policy
        ? call(api.PUT("/admin/approval-policies/{id}", { params: { path: { id: policy.id } }, body }))
        : call(api.POST("/admin/approval-policies", { body }));
    },
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
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={policy ? `Change ${policy.name}` : "New approval policy"}
      description="Requests it applies to wait for each level's approvers, in order."
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save policy"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
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
          className="font-mono text-secondary"
          rows={2}
          value={f.condition}
          onChange={(e) => setF({ ...f, condition: e.target.value })}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Kind of item" htmlFor="ap-type">
          <Select
            id="ap-type"
            value={f.item_type ?? "all"}
            onChange={(v) => setF({ ...f, item_type: v === "all" ? null : (v as S["CatalogItemType"]) })}
            options={itemTypes}
          />
        </Field>
        <Field label="Project" htmlFor="ap-project" error={errors["/project_id"]}>
          <Select
            id="ap-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options("Every project")}
          />
        </Field>
      </div>
      <Field
        label="Approved at once when (CEL, optional)"
        htmlFor="ap-auto"
        error={errors["/auto_approve"]}
        hint="For example a small VM in dev: nobody needs to approve it"
      >
        <Input
          id="ap-auto"
          className="font-mono text-secondary"
          value={f.auto_approve ?? ""}
          onChange={(e) => setF({ ...f, auto_approve: e.target.value || null })}
        />
      </Field>
      <LevelsEditor
        value={f.levels}
        onChange={(levels) => setF({ ...f, levels })}
        errors={errors}
        roles={listed.data?.level_roles ?? []}
      />
      <label htmlFor="ap-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="ap-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
        Enabled
      </label>
    </Sheet>
  );
}

export function ApprovalPolicies({ rights }: { rights: PolicyRights }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.approvals());
  const approversText = (p: Policy) => p.levels.map((l) => askedText(l, policies.data?.level_roles ?? [])).join(" → ");
  const projects = useProjects();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [ranking, setRanking] = useState<Policy | null>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const ops = useOrderedPolicies<Policy>("approval-policies", put);
  const remove = useMutation({
    mutationFn: (p: Policy) => call(api.DELETE("/admin/approval-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void qc.invalidateQueries({ queryKey: ["admin", "approval-policies"] });
      toast.success(`${p.name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const items = policies.data ? [...policies.data.items].sort((a, b) => a.priority - b.priority) : undefined;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New approval policy
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
            {p.item_type && `, ${itemTypes.find((t) => t.value === p.item_type)?.label ?? p.item_type}`}
          </div>
        </>
      ),
    },
    {
      id: "condition",
      header: "Applies when",
      cell: (p) => <span className="font-mono text-secondary break-all">{p.condition}</span>,
    },
    {
      id: "approvers",
      header: "Approvers",
      cell: (p) => <span className="text-secondary">{approversText(p)}</span>,
    },
    { id: "priority", header: "Priority", align: "end", cell: (p) => <span className="tnum">{p.priority}</span> },
  ];
  return (
    <Panel
      title="Approval policies"
      description={`Every policy that matches a request adds its levels of approvers, lower priority first.${
        rights.global ? "" : " You change your projects' own: they add levels to those of every project."
      }`}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label="Approval policies"
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={720}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title="No approval policy"
            body="Every request runs at once. Add a policy to have some approved first."
            action={rights.any && add}
          />
        }
        rowMenuLabel={(p) => `Actions for ${p.name}`}
        rowMenu={
          rights.any && items
            ? (p) =>
                rights.writes(p.project_id) ? (
                  <OrderedMenu
                    policy={p}
                    items={items}
                    index={items.indexOf(p)}
                    ops={ops}
                    onChange={() => setEditing(p)}
                    onPriority={() => setRanking(p)}
                    onRemove={() => setRemoving(p)}
                    moveWhy={movesEveryone(rights)}
                  />
                ) : null
            : undefined
        }
      />
      {editing && <ApprovalSheet policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {ranking && <PriorityDialog policy={ranking} ops={ops} onClose={() => setRanking(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description={`Requests stop waiting for ${approversText(removing)} because of it.`}
          consequences="Requests already waiting keep the approvals they were given. Another policy that matches still applies."
          confirmLabel="Remove policy"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
