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
import { Field, FieldNote, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { formatList } from "@/lib/format";
import { roleName } from "@/lib/i18n/names";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, priorityOf, writable } from "../common";
import { actionName, day2Actions, isBuiltIn } from "./actions";
import {
  enforcementOptions,
  LevelsEditor,
  list,
  movesEveryone,
  OrderedMenu,
  PriorityDialog,
  RoleChecks,
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

const mono = { mono: <span className="font-mono" /> };

/** A policy's actions by name: a built-in one with its key beside it, `*` as every action, another pattern as what it matches. */
function ActionNames({ actions }: { actions: string[] }) {
  const { t } = useTranslation("admin-governance");
  return (
    <ul className="flex flex-col gap-0.5 text-secondary">
      {actions.map((a) => (
        <li key={a}>
          {a === "*" ? (
            t("policies.day2.everyAction")
          ) : a.includes("*") ? (
            <Trans t={t} i18nKey="policies.day2.matching" values={{ pattern: a }} components={mono} />
          ) : isBuiltIn(a) ? (
            <>
              {actionName(a)} <span className="font-mono text-caption text-faint">{a}</span>
            </>
          ) : (
            <span className="font-mono">{a}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

/**
 * The actions a policy decides: every action, or the built-in ones ticked
 * and the keys or patterns typed beside them, such as an AWX action's or
 * `snapshot_*`.
 */
function ActionChecks({
  every,
  picked,
  other,
  onChange,
  error,
}: {
  every: boolean;
  picked: string[];
  other: string;
  onChange: (next: { every?: boolean; picked?: string[]; other?: string }) => void;
  error?: string;
}) {
  const { t } = useTranslation("admin-governance");
  const note = "d2-actions-note";
  return (
    <fieldset className="flex flex-col gap-2" aria-describedby={note}>
      <legend className="mb-1 text-secondary font-medium text-fg">{t("policies.day2.actions")}</legend>
      <div className="flex items-center gap-1.5">
        <Checkbox
          id="d2-every"
          checked={every}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? note : undefined}
          onChange={(on) => onChange({ every: on })}
        />
        <label htmlFor="d2-every" className="text-body">
          {t("policies.day2.everyAction")}
        </label>
      </div>
      {!every && (
        <>
          <div className="grid grid-cols-1 gap-x-4 gap-y-1.5 sm:grid-cols-2">
            {day2Actions.map((a) => (
              <div key={a} className="flex items-start gap-1.5">
                <Checkbox
                  id={`d2-action-${a}`}
                  className="mt-0.5"
                  checked={picked.includes(a)}
                  aria-describedby={`d2-action-${a}-key`}
                  onChange={(on) => onChange({ picked: on ? [...picked, a] : picked.filter((k) => k !== a) })}
                />
                <div className="flex flex-col">
                  <label htmlFor={`d2-action-${a}`} className="text-body">
                    {actionName(a)}
                  </label>
                  <span id={`d2-action-${a}-key`} className="font-mono text-caption text-faint">
                    {a}
                  </span>
                </div>
              </div>
            ))}
          </div>
          <Field
            label={t("policies.day2.otherActions")}
            htmlFor="d2-other"
            hint={<Trans t={t} i18nKey="policies.day2.otherActionsHint" components={mono} />}
          >
            <Input
              id="d2-other"
              className="font-mono text-secondary"
              value={other}
              onChange={(e) => onChange({ other: e.target.value })}
            />
          </Field>
        </>
      )}
      <FieldNote id={note} error={error} />
    </fieldset>
  );
}

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
  const [actions, setActions] = useState(() => {
    const held = f.actions ?? [];
    return {
      every: held.includes("*"),
      picked: held.filter(isBuiltIn),
      other: held.filter((a) => a !== "*" && !isBuiltIn(a)).join(", "),
    };
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...f,
        actions: actions.every
          ? ["*"]
          : [...day2Actions.filter((a) => actions.picked.includes(a)), ...list(actions.other)],
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
      submitLabel={save.isPending ? t("policies.saving") : t(policy ? "policies.savePolicy" : "policies.createPolicy")}
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
      <ActionChecks
        {...actions}
        onChange={(next) => setActions({ ...actions, ...next })}
        error={errorAt(errors, "/actions")}
      />
      <RoleChecks
        id="d2-role"
        legend={t("policies.day2.roles")}
        roles={listed.data?.policy_roles ?? []}
        picked={f.roles ?? []}
        onChange={(roles) => setF({ ...f, roles })}
        hint={t("policies.day2.rolesHint")}
        error={errorAt(errors, "/roles")}
      />
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
  const roleOf = (key: string) => {
    const r = policies.data?.policy_roles.find((x) => x.key === key);
    return r ? roleName(r) : key;
  };
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
      cell: (p) => <ActionNames actions={p.actions ?? []} />,
    },
    {
      id: "who",
      header: t("policies.day2.whoAndWhen"),
      cell: (p) => (
        <span className="text-secondary">
          {p.roles?.length ? formatList(p.roles.map(roleOf), "disjunction") : t("policies.day2.anyone")}
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
