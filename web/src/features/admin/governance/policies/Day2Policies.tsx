import { useMutation, useQuery } from "@tanstack/react-query";
import { Wrench } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import {
  enforcementOptions,
  LevelsEditor,
  list,
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
type Policy = S["Day2Policy"];

const effectTone = { allow: "ok", deny: "bad", approval: "warn" } as const;

const put = ({ id, set_by_admin: _set, ...body }: Policy) =>
  call(api.PUT("/admin/day2-policies/{id}", { params: { path: { id } }, body }));

function Day2Sheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("day2-policies");
  const projects = useProjects();
  const listed = useQuery(adm.day2());
  const [f, setF] = useState<S["Day2PolicyWrite"]>(
    policy
      ? writable(policy, "id", "set_by_admin")
      : {
          name: "",
          reason: "",
          priority: 100,
          actions: ["*"],
          roles: [],
          condition: "true",
          effect: "allow",
          levels: [],
          project_id: projects.initial,
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
        enforcement: f.effect === "allow" ? undefined : (f.enforcement ?? "hard"),
        priority: priorityOf(priority),
      };
      return policy
        ? call(api.PUT("/admin/day2-policies/{id}", { params: { path: { id: policy.id } }, body }))
        : call(api.POST("/admin/day2-policies", { body }));
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
      title={policy ? t("policies.changeNamed", { name: policy.name }) : t("policies.day2.new")}
      description={t("policies.day2.sheetLead")}
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? t("policies.saving") : t("policies.savePolicy")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label={t("policies.name")} htmlFor="d2-name" error={errors["/name"]}>
          <Input id="d2-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label={t("policies.priority")} htmlFor="d2-priority" hint={t("policies.lowerFirst")}>
          <Input id="d2-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <Field label={t("policies.day2.effectLabel")} htmlFor="d2-effect">
        <Select
          id="d2-effect"
          value={f.effect}
          onChange={(v) =>
            setF({
              ...f,
              effect: v as S["Day2PolicyWrite"]["effect"],
              levels:
                v === "approval" && !f.levels?.length
                  ? [{ name: t("policies.levels.named", { n: 1 }), mode: "any" }]
                  : f.levels,
            })
          }
          options={[
            { value: "allow", label: t("policies.day2.effect.allow") },
            { value: "deny", label: t("policies.day2.effect.deny") },
            { value: "approval", label: t("policies.day2.effect.approval") },
          ]}
        />
      </Field>
      {f.effect !== "allow" && (
        <Field
          label={t("policies.enforcementLabel")}
          htmlFor="d2-enforcement"
          error={errors["/enforcement"]}
          hint={t("policies.day2.enforcementHint")}
        >
          <Select
            id="d2-enforcement"
            value={f.enforcement ?? "hard"}
            onChange={(v) => setF({ ...f, enforcement: v as S["PolicyEnforcement"] })}
            options={enforcementOptions()}
          />
        </Field>
      )}
      <Field
        label={t("policies.day2.actions")}
        htmlFor="d2-actions"
        error={errorAt(errors, "/actions")}
        hint={t("policies.day2.actionsHint", { example: "snapshot_*, delete" })}
      >
        <Input
          id="d2-actions"
          className="font-mono text-secondary"
          value={actions}
          onChange={(e) => setActions(e.target.value)}
        />
      </Field>
      <Field
        label={t("policies.day2.roles")}
        htmlFor="d2-roles"
        error={errorAt(errors, "/roles")}
        hint={t("policies.day2.rolesHint", { roles: "owner, member, project-admin, supervisor, admin" })}
      >
        <Input
          id="d2-roles"
          className="font-mono text-secondary"
          value={roles}
          onChange={(e) => setRoles(e.target.value)}
        />
      </Field>
      <Field
        label={t("policies.appliesWhenCel")}
        htmlFor="d2-condition"
        error={errors["/condition"]}
        hint={t("policies.day2.conditionHint", { example: "environment.production" })}
      >
        <Textarea
          id="d2-condition"
          className="font-mono text-secondary"
          rows={2}
          value={f.condition}
          onChange={(e) => setF({ ...f, condition: e.target.value })}
        />
      </Field>
      <Field label={t("policies.day2.reason")} htmlFor="d2-reason">
        <Input id="d2-reason" value={f.reason ?? ""} onChange={(e) => setF({ ...f, reason: e.target.value })} />
      </Field>
      <Field
        label={t("policies.project")}
        htmlFor="d2-project"
        error={errors["/project_id"]}
        hint={t("policies.day2.projectHint")}
      >
        <Select
          id="d2-project"
          value={f.project_id ?? "all"}
          onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
          options={projects.options(t("policies.everyProject"))}
        />
      </Field>
      {f.effect === "approval" && (
        <LevelsEditor
          value={f.levels ?? []}
          onChange={(levels) => setF({ ...f, levels })}
          errors={errors}
          roles={listed.data?.level_roles ?? []}
        />
      )}
      <label htmlFor="d2-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="d2-enabled" checked={f.enabled} onChange={(enabled) => setF({ ...f, enabled })} />
        {t("policies.enabled")}
      </label>
    </Sheet>
  );
}

export function Day2Policies({ rights }: { rights: PolicyRights }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("day2-policies");
  const policies = useQuery(adm.day2());
  const projects = useProjects();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [ranking, setRanking] = useState<Policy | null>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const ops = useOrderedPolicies<Policy>("day2-policies", put);
  const remove = useMutation({
    mutationFn: (p: Policy) => call(api.DELETE("/admin/day2-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void refresh();
      toast.success(t("policies.removed", { name: p.name }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const items = policies.data ? [...policies.data.items].sort((a, b) => a.priority - b.priority) : undefined;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("policies.day2.new")}
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
          </div>
          <SetByAdmin policy={p} />
        </>
      ),
    },
    {
      id: "actions",
      header: t("policies.day2.actions"),
      cell: (p) => <span className="font-mono text-secondary">{(p.actions ?? []).join(", ")}</span>,
    },
    {
      id: "who",
      header: t("policies.day2.whoAndWhen"),
      cell: (p) => (
        <span className="text-secondary">
          {(p.roles ?? []).length ? (p.roles ?? []).join(", ") : t("policies.day2.anyone")}
          {p.condition !== "true" && <span className="block font-mono text-muted">{p.condition}</span>}
        </span>
      ),
    },
    {
      id: "effect",
      header: t("policies.day2.effectLabel"),
      cell: (p) => (
        <span className="flex flex-wrap gap-1">
          <Badge tone={effectTone[p.effect]}>{t(`policies.day2.effect.${p.effect}`)}</Badge>
          {p.enforcement === "soft" && <Badge tone="outline">{t("policies.softWarns")}</Badge>}
        </span>
      ),
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
      title={t("policies.day2.title")}
      description={rights.global ? t("policies.day2.lead") : t("policies.day2.leadOwn")}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.day2.title")}
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={760}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Wrench}
            title={t("policies.day2.emptyTitle")}
            body={t("policies.day2.emptyBody")}
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
      {editing && <Day2Sheet policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {ranking && <PriorityDialog policy={ranking} ops={ops} onClose={() => setRanking(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("policies.removeNamed", { name: removing.name })}
          description={
            <Trans
              t={t}
              i18nKey="policies.day2.removeLead"
              values={{ actions: (removing.actions ?? []).join(", ") }}
              components={{ mono: <span className="font-mono" /> }}
            />
          }
          consequences={t("policies.day2.removeConsequences")}
          confirmLabel={t("policies.removePolicy")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
