import { useMutation, useQuery } from "@tanstack/react-query";
import { ListFilter } from "lucide-react";
import { useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useEnvName } from "@/lib/environments";
import { adm, q } from "@/lib/query";
import { failed, fieldErrors, priorityOf, writable } from "../common";
import { LeaseSummary, LeaseTermsFields } from "./LeasePolicies";
import { SetByAdmin, useProjects, useRefreshPolicies, type PolicyRights } from "./shared";

type S = components["schemas"];
type Rule = S["LeaseRule"];

function RuleSheet({
  rule,
  onClose,
  returnFocus,
}: {
  rule?: Rule;
  onClose: () => void;
  returnFocus?: () => HTMLElement | null;
}) {
  const { t } = useTranslation("admin-governance");
  const envs = useQuery(q.environments());
  const projects = useProjects();
  const refresh = useRefreshPolicies("lease-rules");
  const [f, setF] = useState<S["LeaseRuleWrite"]>(
    rule
      ? writable(rule, "id", "set_by_admin")
      : {
          name: "",
          condition: "",
          environment: null,
          project_id: projects.initial,
          priority: 100,
          default_days: 7,
          max_days: 30,
          max_extensions: 1,
          grace_days: 7,
          expiry_action: "power_off_then_delete",
          enforcement: "hard",
          max_lifetime_days: null,
        },
  );
  const [priority, setPriority] = useState(String(f.priority ?? 100));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = { ...f, priority: priorityOf(priority) };
      return rule
        ? call(api.PUT("/admin/lease-rules/{id}", { params: { path: { id: rule.id } }, body }))
        : call(api.POST("/admin/lease-rules", { body }));
    },
    // The rules are read again before the sheet closes, so the focus can go to what replaces the empty state.
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
      title={rule ? t("policies.changeNamed", { name: rule.name }) : t("policies.rules.new")}
      description={t("policies.rules.sheetLead")}
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? t("policies.saving") : t(rule ? "policies.rules.save" : "policies.rules.create")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label={t("policies.name")} htmlFor="lr-name" error={errors["/name"]}>
          <Input id="lr-name" value={f.name} maxLength={120} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label={t("policies.priority")} htmlFor="lr-priority" hint={t("policies.lowerFirst")}>
          <Input id="lr-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <Field
        label={t("policies.appliesWhenCel")}
        htmlFor="lr-condition"
        error={errors["/condition"]}
        hint={t("policies.rules.conditionHint", { first: 'item.slug == "gpu-server"', second: 'inputs.size == "S"' })}
      >
        <Textarea
          id="lr-condition"
          className="font-mono text-secondary"
          rows={2}
          value={f.condition}
          onChange={(e) => setF({ ...f, condition: e.target.value })}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("policies.environment")} htmlFor="lr-env" error={errors["/environment"]}>
          <Select
            id="lr-env"
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
          htmlFor="lr-project"
          error={errors["/project_id"]}
          hint={t("policies.rules.projectHint")}
        >
          <Select
            id="lr-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options(t("policies.everyProject"))}
          />
        </Field>
      </div>
      <LeaseTermsFields id="lr" f={f} setF={setF} errors={errors} />
    </Sheet>
  );
}

export function LeaseRules({ rights }: { rights: PolicyRights }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("lease-rules");
  const rules = useQuery(adm.leaseRules());
  const envName = useEnvName();
  const projects = useProjects();
  const [editing, setEditing] = useState<Rule | "new" | null>(null);
  const [fromEmpty, setFromEmpty] = useState(false);
  const addRef = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<Rule | null>(null);
  const remove = useMutation({
    mutationFn: (r: Rule) => call(api.DELETE("/admin/lease-rules/{id}", { params: { path: { id: r.id } } })),
    onSuccess: (_, r) => {
      void refresh();
      toast.success(t("policies.removed", { name: r.name }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const items = rules.data?.items;
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
      {t("policies.rules.new")}
    </Button>
  );
  const columns: Column<Rule>[] = [
    {
      id: "rule",
      header: t("policies.rules.rule"),
      cell: (r) => (
        <>
          <span className="font-medium">{r.name}</span>
          <div className="text-secondary text-muted">
            {t("policies.whereBoth", {
              environment: r.environment ? envName(r.environment) : t("policies.everyEnvironment"),
              project: r.project_id ? projects.name(r.project_id) : t("policies.everyProjectInText"),
            })}
          </div>
          <SetByAdmin policy={r} />
        </>
      ),
    },
    {
      id: "condition",
      header: t("policies.appliesWhen"),
      cell: (r) => <span className="font-mono text-secondary">{r.condition}</span>,
    },
    { id: "lease", header: t("policies.lease.lease"), cell: (r) => <LeaseSummary lease={r} /> },
    {
      id: "extensions",
      header: t("policies.lease.extensions"),
      align: "end",
      cell: (r) => <span className="tnum">{r.max_extensions}</span>,
    },
    {
      id: "priority",
      header: t("policies.priority"),
      align: "end",
      cell: (r) => <span className="tnum">{r.priority}</span>,
    },
  ];
  return (
    <Panel
      title={t("policies.rules.title")}
      description={rights.global || !rights.any ? t("policies.rules.lead") : t("policies.rules.leadOwn")}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.rules.title")}
        columns={columns}
        rows={items}
        rowKey={(r) => r.id}
        minWidth={760}
        loading={rules.isPending}
        error={rules.isError && <ErrorState error={rules.error} onRetry={() => void rules.refetch()} />}
        empty={
          <EmptyState
            icon={ListFilter}
            title={t("policies.rules.emptyTitle")}
            body={rights.any ? t("policies.rules.emptyBodyAdd") : t("policies.rules.emptyBody")}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(r) => t("policies.actionsFor", { name: r.name })}
        rowMenu={
          rights.any
            ? (r) =>
                rights.changes(r) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(r)}>{t("policies.menu.change")}</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(r)}>
                      {t("policies.menu.remove")}
                    </MenuItem>
                  </>
                ) : null
            : undefined
        }
      />
      {editing && (
        <RuleSheet
          rule={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          returnFocus={editing === "new" && fromEmpty ? () => addRef.current : undefined}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("policies.removeNamed", { name: removing.name })}
          description={
            <Trans
              t={t}
              i18nKey="policies.rules.removeLead"
              values={{ condition: removing.condition }}
              components={{ mono: <span className="font-mono" /> }}
            />
          }
          consequences={
            removing.project_id && !removing.set_by_admin
              ? t("policies.rules.removeConsequencesHeld")
              : t("policies.rules.removeConsequences")
          }
          confirmLabel={t("policies.rules.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
