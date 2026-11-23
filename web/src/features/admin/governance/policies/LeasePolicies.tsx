import { useMutation, useQuery } from "@tanstack/react-query";
import { Timer } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useEnvName } from "@/lib/environments";
import { i18n } from "@/lib/i18n";
import { adm, q } from "@/lib/query";
import { failed, fieldErrors, writable } from "../common";
import { enforcementOptions, SetByAdmin, useProjects, useRefreshPolicies, type PolicyRights } from "./shared";

type S = components["schemas"];
type Lease = S["LeasePolicy"];

const expiryActions: S["LeaseExpiryAction"][] = ["power_off_then_delete", "delete", "notify_only"];

export const expiryLabel = (a: S["LeaseExpiryAction"]) => i18n.t(`admin-governance:policies.lease.expiry.${a}`);

type Terms = Pick<
  Lease,
  "default_days" | "max_days" | "max_extensions" | "grace_days" | "expiry_action" | "enforcement" | "max_lifetime_days"
>;

/** What a lease policy or rule gives, in a table cell. */
export function LeaseSummary({ lease }: { lease: Terms }) {
  const { t } = useTranslation("admin-governance");
  return (
    <span className="text-secondary">
      {lease.default_days
        ? t("policies.lease.summary", {
            count: lease.default_days,
            max: lease.max_days ?? t("policies.lease.noLimit"),
          })
        : t("policies.lease.none")}
      {lease.max_lifetime_days != null && (
        <span className="block text-muted">{t("policies.lease.lifetime", { count: lease.max_lifetime_days })}</span>
      )}
      {lease.enforcement === "soft" && <span className="block text-muted">{t("policies.lease.softLonger")}</span>}
    </span>
  );
}

/** The fields of what a lease policy or rule gives (ADR-0014, ADR-0048). */
export function LeaseTermsFields<T extends Terms>({
  id,
  f,
  setF,
  errors,
}: {
  id: string;
  f: T;
  setF: (f: T) => void;
  errors: Record<string, string>;
}) {
  const { t } = useTranslation("admin-governance");
  const days = (k: "default_days" | "max_days" | "max_lifetime_days", label: string, hint: string) => (
    <Field label={label} htmlFor={`${id}-${k}`} error={errors[`/${k}`]} hint={hint}>
      <Input
        id={`${id}-${k}`}
        type="number"
        min={1}
        value={f[k] ?? ""}
        onChange={(e) => setF({ ...f, [k]: e.target.value === "" ? null : Number(e.target.value) })}
      />
    </Field>
  );
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        {days("default_days", t("policies.lease.leaseDays"), t("policies.lease.emptyForNone"))}
        {days("max_days", t("policies.lease.maxDays"), t("policies.lease.emptyForNone"))}
        <Field label={t("policies.lease.extensions")} htmlFor={`${id}-ext`} error={errors["/max_extensions"]}>
          <Input
            id={`${id}-ext`}
            type="number"
            min={0}
            value={f.max_extensions}
            onChange={(e) => setF({ ...f, max_extensions: Number(e.target.value) })}
          />
        </Field>
        <Field label={t("policies.lease.grace")} htmlFor={`${id}-grace`} error={errors["/grace_days"]}>
          <Input
            id={`${id}-grace`}
            type="number"
            min={0}
            value={f.grace_days}
            onChange={(e) => setF({ ...f, grace_days: Number(e.target.value) })}
          />
        </Field>
      </div>
      {days("max_lifetime_days", t("policies.lease.lifetimeDays"), t("policies.lease.lifetimeHint"))}
      <Field label={t("policies.lease.whenExpires")} htmlFor={`${id}-action`} error={errors["/expiry_action"]}>
        <Select
          id={`${id}-action`}
          value={f.expiry_action}
          onChange={(v) => setF({ ...f, expiry_action: v as S["LeaseExpiryAction"] })}
          options={expiryActions.map((value) => ({ value, label: expiryLabel(value) }))}
        />
      </Field>
      <Field
        label={t("policies.enforcementLabel")}
        htmlFor={`${id}-enforcement`}
        error={errors["/enforcement"]}
        hint={t("policies.lease.enforcementHint")}
      >
        <Select
          id={`${id}-enforcement`}
          value={f.enforcement ?? "hard"}
          onChange={(v) => setF({ ...f, enforcement: v as S["PolicyEnforcement"] })}
          options={enforcementOptions()}
        />
      </Field>
    </>
  );
}

function LeaseSheet({ lease, onClose }: { lease?: Lease; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const envs = useQuery(q.environments());
  const projects = useProjects();
  const refresh = useRefreshPolicies("leases");
  const [f, setF] = useState<Lease>(
    lease
      ? writable(lease, "set_by_admin")
      : {
          environment: "dev",
          project_id: projects.initial,
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
      void refresh();
      toast.success(t("policies.lease.saved"));
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
      title={lease ? t("policies.lease.change") : t("policies.lease.new")}
      description={t("policies.lease.sheetLead")}
      submitLabel={save.isPending ? t("policies.saving") : t(lease ? "policies.lease.save" : "policies.lease.create")}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("policies.environment")} htmlFor="lease-env" error={errors["/environment"]}>
          <Select
            id="lease-env"
            value={f.environment}
            disabled={Boolean(lease)}
            onChange={(v) => setF({ ...f, environment: v })}
            options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
          />
        </Field>
        <Field
          label={t("policies.project")}
          htmlFor="lease-project"
          error={errors["/project_id"]}
          hint={t("policies.lease.projectHint")}
        >
          <Select
            id="lease-project"
            value={f.project_id ?? "all"}
            disabled={Boolean(lease)}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options(t("policies.everyProject"))}
          />
        </Field>
      </div>
      <LeaseTermsFields id="lease" f={f} setF={setF} errors={errors} />
    </Sheet>
  );
}

export function LeasePolicies({ rights }: { rights: PolicyRights }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies("leases");
  const leases = useQuery(adm.leases());
  const projects = useProjects();
  const envName = useEnvName();
  const [editing, setEditing] = useState<Lease | "new" | null>(null);
  const [removing, setRemoving] = useState<Lease | null>(null);
  const where = (l: Lease) => `${envName(l.environment)}${l.project_id ? `, ${projects.name(l.project_id)}` : ""}`;
  const remove = useMutation({
    mutationFn: (l: Lease) =>
      call(
        api.DELETE("/admin/lease-policies", {
          params: { query: { environment: l.environment, project_id: l.project_id ?? "" } },
        }),
      ),
    onSuccess: (_, l) => {
      void refresh();
      toast.success(t("policies.lease.removed", { where: where(l) }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const items = leases.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("policies.lease.new")}
    </Button>
  );
  const columns: Column<Lease>[] = [
    {
      id: "where",
      header: t("policies.lease.where"),
      sortValue: (l) => where(l),
      cell: (l) => (
        <>
          <span className="font-medium">{envName(l.environment)}</span>
          <div className="text-secondary text-muted">
            {l.project_id ? projects.name(l.project_id) : t("policies.everyProject")}
          </div>
          <SetByAdmin policy={l} />
        </>
      ),
    },
    {
      id: "lease",
      header: t("policies.lease.lease"),
      cell: (l) => <LeaseSummary lease={l} />,
    },
    {
      id: "extensions",
      header: t("policies.lease.extensions"),
      align: "end",
      cell: (l) => <span className="tnum">{l.max_extensions}</span>,
    },
    {
      id: "expiry",
      header: t("policies.lease.whenExpires"),
      cell: (l) => (
        <span className="text-secondary text-muted">
          {t("policies.lease.expiryGrace", { action: expiryLabel(l.expiry_action), count: l.grace_days })}
        </span>
      ),
    },
  ];
  return (
    <Panel
      title={t("policies.lease.title")}
      description={rights.global ? t("policies.lease.lead") : t("policies.lease.leadOwn")}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.lease.title")}
        columns={columns}
        rows={items}
        rowKey={(l) => `${l.environment}/${l.project_id ?? ""}`}
        minWidth={680}
        loading={leases.isPending}
        error={leases.isError && <ErrorState error={leases.error} onRetry={() => void leases.refetch()} />}
        empty={
          <EmptyState
            icon={Timer}
            title={t("policies.lease.emptyTitle")}
            body={t("policies.lease.emptyBody")}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(l) => t("policies.lease.actionsFor", { where: where(l) })}
        rowMenu={
          rights.any
            ? (l) =>
                rights.changes(l) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(l)}>{t("policies.menu.change")}</MenuItem>
                    {l.project_id && (
                      <>
                        <MenuSeparator />
                        <MenuItem danger onSelect={() => setRemoving(l)}>
                          {t("policies.menu.remove")}
                        </MenuItem>
                      </>
                    )}
                  </>
                ) : null
            : undefined
        }
      />
      {editing && <LeaseSheet lease={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("policies.lease.removeTitle", { where: where(removing) })}
          description={t("policies.lease.removeLead", {
            project: projects.name(removing.project_id ?? ""),
            environment: envName(removing.environment),
          })}
          consequences={t("policies.lease.removeConsequences", { environment: envName(removing.environment) })}
          confirmLabel={t("policies.lease.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
