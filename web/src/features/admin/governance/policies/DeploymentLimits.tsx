import { useMutation, useQuery } from "@tanstack/react-query";
import { Gauge } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { limitKinds, limitLabel, useEnvName } from "@/features/requests/limits";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { adm, q } from "@/lib/query";
import { failed, fieldErrors, priorityOf, writable } from "../common";
import { enforcementOptions, SetByAdmin, useProjects, useRefreshPolicies, type PolicyRights } from "./shared";

type S = components["schemas"];
type Policy = S["DeploymentLimitPolicy"];
type Kind = S["DeploymentLimitKind"];

const put = (p: Policy) =>
  call(
    api.PUT("/admin/deployment-limit-policies/{id}", {
      params: { path: { id: p.id } },
      body: writable(p, "id", "set_by_admin"),
    }),
  );

/** A policy's limits in words, one a line. */
export function limitsText(p: Policy): string[] {
  return limitKinds.flatMap((k) => {
    const v = p[k];
    if (v == null) return [];
    const [name, unit] = limitLabel[k];
    return [
      unit
        ? i18n.t("admin-governance:policies.limits.lineUnit", { name, value: v, unit })
        : i18n.t("admin-governance:policies.limits.line", { name, value: v }),
    ];
  });
}

const kindName = (k: Kind) => i18n.t(`admin-governance:policies.limits.kind.${k}`);

const number = (s: string) => (s.trim() === "" ? null : Number(s));

function PolicySheet({
  policy,
  onClose,
  returnFocus,
}: {
  policy?: Policy;
  onClose: () => void;
  returnFocus?: () => HTMLElement | null;
}) {
  const { t } = useTranslation("admin-governance");
  const envs = useQuery(q.environments());
  const projects = useProjects();
  const refresh = useRefreshPolicies("deployment-limit-policies");
  const [f, setF] = useState<S["DeploymentLimitPolicyWrite"]>(
    policy
      ? writable(policy, "id", "set_by_admin")
      : {
          name: "",
          project_id: projects.initial,
          environment: null,
          priority: 100,
          enabled: true,
          enforcement: "hard",
        },
  );
  const [priority, setPriority] = useState(String(f.priority ?? 100));
  const [limits, setLimits] = useState<Record<Kind, string>>(
    () => Object.fromEntries(limitKinds.map((k) => [k, f[k] == null ? "" : String(f[k])])) as Record<Kind, string>,
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...f,
        priority: priorityOf(priority),
        ...Object.fromEntries(limitKinds.map((k) => [k, number(limits[k])])),
      };
      return policy
        ? call(api.PUT("/admin/deployment-limit-policies/{id}", { params: { path: { id: policy.id } }, body }))
        : call(api.POST("/admin/deployment-limit-policies", { body }));
    },
    // The policies are read again before the sheet closes, so the focus can go to what replaces the empty state.
    onSuccess: async () => {
      await refresh();
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
      returnFocus={returnFocus}
      title={policy ? t("policies.changeNamed", { name: policy.name }) : t("policies.limits.new")}
      description={t("policies.limits.sheetLead")}
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? t("policies.saving") : t("policies.savePolicy")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label={t("policies.name")} htmlFor="dl-name" error={errors["/name"]}>
          <Input id="dl-name" value={f.name} maxLength={120} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label={t("policies.priority")} htmlFor="dl-priority" hint={t("policies.lowerFirst")}>
          <Input id="dl-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("policies.environment")} htmlFor="dl-env" error={errors["/environment"]}>
          <Select
            id="dl-env"
            value={f.environment ?? "all"}
            onChange={(v) => setF({ ...f, environment: v === "all" ? null : v })}
            options={[
              { value: "all", label: t("policies.everyEnvironment") },
              ...(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name })),
            ]}
          />
        </Field>
        <Field
          label={t("policies.project")}
          htmlFor="dl-project"
          error={errors["/project_id"]}
          hint={t("policies.limits.projectHint")}
        >
          <Select
            id="dl-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options(t("policies.everyProject"))}
          />
        </Field>
      </div>
      <Field label={t("policies.enforcementLabel")} htmlFor="dl-enforcement">
        <Select
          id="dl-enforcement"
          value={f.enforcement ?? "hard"}
          onChange={(v) => setF({ ...f, enforcement: v as S["PolicyEnforcement"] })}
          options={enforcementOptions()}
        />
      </Field>
      <fieldset className="flex flex-col gap-2">
        <legend className="label mb-1">{t("policies.limits.limits")}</legend>
        <div className="grid grid-cols-2 gap-3">
          {limitKinds.map((k) => {
            const [name, unit] = limitLabel[k];
            return (
              <Field
                key={k}
                label={unit ? t("policies.limits.withUnit", { name, unit }) : name}
                htmlFor={`dl-${k}`}
                error={errors[`/${k}`]}
              >
                <Input
                  id={`dl-${k}`}
                  type="number"
                  placeholder={t("policies.limits.noLimit")}
                  value={limits[k]}
                  onChange={(e) => setLimits({ ...limits, [k]: e.target.value })}
                />
              </Field>
            );
          })}
        </div>
        <FieldNote error={errors[""]} />
      </fieldset>
      <label htmlFor="dl-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="dl-enabled" checked={f.enabled ?? true} onChange={(enabled) => setF({ ...f, enabled })} />
        {t("policies.enabled")}
      </label>
    </Sheet>
  );
}

/** Deployment limit policies (ADR-0047): global ones and those of the caller's projects. */
export function DeploymentLimits({ rights }: { rights: PolicyRights }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("deployment-limit-policies");
  const policies = useQuery(adm.limitPolicies());
  const projects = useProjects();
  const envName = useEnvName();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [fromEmpty, setFromEmpty] = useState(false);
  const addRef = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const remove = useMutation({
    mutationFn: (p: Policy) =>
      call(api.DELETE("/admin/deployment-limit-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void refresh();
      toast.success(t("policies.removed", { name: p.name }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const turn = useMutation({
    mutationFn: (p: Policy) => put({ ...p, enabled: !p.enabled }),
    onSuccess: (_, p) => toast.success(t(p.enabled ? "policies.turnedOff" : "policies.turnedOn", { name: p.name })),
    onSettled: refresh,
    onError: (e) => failed(e, t("policies.notSaved")),
  });
  const items = policies.data?.items;
  const add = (
    <Button
      ref={addRef}
      size="sm"
      variant="primary"
      onClick={() => {
        setFromEmpty(!items?.length);
        setEditing("new");
      }}
    >
      {t("policies.limits.new")}
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
            {t("policies.whereBoth", {
              environment: p.environment ? envName(p.environment) : t("policies.everyEnvironment"),
              project: p.project_id ? projects.name(p.project_id) : t("policies.everyProjectInText"),
            })}
          </div>
          <SetByAdmin policy={p} />
        </>
      ),
    },
    {
      id: "limits",
      header: t("policies.limits.limits"),
      cell: (p) => (
        <ul className="flex flex-col text-secondary">
          {limitsText(p).map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      ),
    },
    {
      id: "enforcement",
      header: t("policies.enforcementLabel"),
      cell: (p) => (p.enforcement === "soft" ? t("policies.softWarns") : t("policies.hardRefuses")),
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
      title={t("policies.limits.title")}
      description={rights.global || !rights.any ? t("policies.limits.lead") : t("policies.limits.leadOwn")}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.limits.table")}
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={760}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Gauge}
            title={t("policies.limits.emptyTitle")}
            body={rights.any ? t("policies.limits.emptyBodyAdd") : t("policies.limits.emptyBody")}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(p) => t("policies.actionsFor", { name: p.name })}
        rowMenu={
          rights.any
            ? (p) =>
                rights.changes(p) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(p)}>{t("policies.menu.change")}</MenuItem>
                    <MenuItem disabled={turn.isPending} onSelect={() => turn.mutate(p)}>
                      {p.enabled ? t("policies.menu.turnOff") : t("policies.menu.turnOn")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(p)}>
                      {t("policies.menu.remove")}
                    </MenuItem>
                  </>
                ) : null
            : undefined
        }
      />
      {editing && (
        <PolicySheet
          policy={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          returnFocus={editing === "new" && fromEmpty ? () => addRef.current : undefined}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("policies.removeNamed", { name: removing.name })}
          description={t("policies.limits.removeLead", {
            kinds: formatList(limitKinds.filter((k) => removing[k] != null).map(kindName)),
          })}
          consequences={t("policies.limits.removeConsequences")}
          confirmLabel={t("policies.removePolicy")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
