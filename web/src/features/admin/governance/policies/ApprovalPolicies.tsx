import { useMutation, useQuery } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
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
import { i18n } from "@/lib/i18n";
import { adm } from "@/lib/query";
import { failed, fieldErrors, priorityOf, writable } from "../common";
import {
  askedText,
  LevelsEditor,
  movesEveryone,
  OrderedMenu,
  PriorityDialog,
  SetByAdmin,
  useOrderedPolicies,
  useProjects,
  useRefreshPolicies,
  type PolicyRights,
} from "./shared";

type S = components["schemas"];
type Policy = S["ApprovalPolicy"];

const itemTypeValues = ["all", "vm_blueprint", "awx_job_template", "awx_workflow", "composite"] as const;

const itemTypes = () =>
  itemTypeValues.map((value) => ({ value, label: i18n.t(`admin-governance:policies.approval.itemType.${value}`) }));

const put = ({ id, set_by_admin: _set, ...body }: Policy) =>
  call(api.PUT("/admin/approval-policies/{id}", { params: { path: { id } }, body }));

function ApprovalSheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("approval-policies");
  const projects = useProjects();
  const listed = useQuery(adm.approvals());
  const [f, setF] = useState<S["ApprovalPolicyWrite"]>(
    policy
      ? writable(policy, "id", "set_by_admin")
      : {
          name: "",
          reason: "",
          condition: "true",
          item_type: null,
          project_id: projects.initial,
          levels: [{ name: t("policies.levels.named", { n: 1 }), mode: "any" }],
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
      void refresh();
      toast.success(t("policies.saved", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("policies.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={policy ? t("policies.changeNamed", { name: policy.name }) : t("policies.approval.new")}
      description={t("policies.approval.sheetLead")}
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? t("policies.saving") : t("policies.savePolicy")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label={t("policies.name")} htmlFor="ap-name" error={errors["/name"]}>
          <Input id="ap-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label={t("policies.priority")} htmlFor="ap-priority" hint={t("policies.lowerFirst")}>
          <Input id="ap-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <Field label={t("policies.approval.reason")} htmlFor="ap-reason">
        <Input id="ap-reason" value={f.reason ?? ""} onChange={(e) => setF({ ...f, reason: e.target.value })} />
      </Field>
      <Field
        label={t("policies.appliesWhenCel")}
        htmlFor="ap-condition"
        error={errors["/condition"]}
        hint={t("policies.approval.conditionHint", { example: 'environment == "prd" || inputs.cpu_count > 8' })}
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
        <Field label={t("policies.approval.kind")} htmlFor="ap-type">
          <Select
            id="ap-type"
            value={f.item_type ?? "all"}
            onChange={(v) => setF({ ...f, item_type: v === "all" ? null : (v as S["CatalogItemType"]) })}
            options={itemTypes()}
          />
        </Field>
        <Field label={t("policies.project")} htmlFor="ap-project" error={errors["/project_id"]}>
          <Select
            id="ap-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options(t("policies.everyProject"))}
          />
        </Field>
      </div>
      <Field
        label={t("policies.approval.auto")}
        htmlFor="ap-auto"
        error={errors["/auto_approve"]}
        hint={t("policies.approval.autoHint")}
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
        {t("policies.enabled")}
      </label>
    </Sheet>
  );
}

export function ApprovalPolicies({ rights }: { rights: PolicyRights }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("approval-policies");
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
      void refresh();
      toast.success(t("policies.removed", { name: p.name }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const items = policies.data ? [...policies.data.items].sort((a, b) => a.priority - b.priority) : undefined;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("policies.approval.new")}
    </Button>
  );
  const columns: Column<Policy>[] = [
    {
      id: "policy",
      header: t("policies.policy"),
      cell: (p) => (
        <>
          <span className="font-medium">{p.name}</span>
          {!p.enabled && (
            <Badge tone="neutral" className="ml-2">
              {t("policies.off")}
            </Badge>
          )}
          <div className="text-secondary text-muted">
            {p.project_id ? projects.name(p.project_id) : t("policies.everyProject")}
            {p.item_type && `, ${itemTypes().find((x) => x.value === p.item_type)?.label ?? p.item_type}`}
          </div>
          <SetByAdmin policy={p} />
        </>
      ),
    },
    {
      id: "condition",
      header: t("policies.appliesWhen"),
      cell: (p) => <span className="font-mono text-secondary break-all">{p.condition}</span>,
    },
    {
      id: "approvers",
      header: t("policies.approval.approvers"),
      cell: (p) => <span className="text-secondary">{approversText(p)}</span>,
    },
    {
      id: "priority",
      header: t("policies.priority"),
      align: "end",
      cell: (p) => <span className="tnum">{p.priority}</span>,
    },
  ];
  return (
    <Panel
      title={t("policies.approval.title")}
      description={rights.global ? t("policies.approval.lead") : t("policies.approval.leadOwn")}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.approval.title")}
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={720}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title={t("policies.approval.emptyTitle")}
            body={t("policies.approval.emptyBody")}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(p) => t("policies.actionsFor", { name: p.name })}
        rowMenu={
          rights.any && items
            ? (p) =>
                rights.changes(p) ? (
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
          title={t("policies.removeNamed", { name: removing.name })}
          description={t("policies.approval.removeLead", { approvers: approversText(removing) })}
          consequences={t("policies.approval.removeConsequences")}
          confirmLabel={t("policies.removePolicy")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
