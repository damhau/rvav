import { useMutation, useQuery } from "@tanstack/react-query";
import { Timer } from "lucide-react";
import { useState } from "react";
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
import { adm, q } from "@/lib/query";
import { failed, fieldErrors, writable } from "../common";
import { enforcementOptions, SetByAdmin, useProjects, useRefreshPolicies, type PolicyRights } from "./shared";

type S = components["schemas"];
type Lease = S["LeasePolicy"];

export const expiryLabels: Record<S["LeaseExpiryAction"], string> = {
  power_off_then_delete: "Power off, then delete after the grace period",
  delete: "Delete after the grace period",
  notify_only: "Only tell the owner",
};

type Terms = Pick<
  Lease,
  "default_days" | "max_days" | "max_extensions" | "grace_days" | "expiry_action" | "enforcement" | "max_lifetime_days"
>;

/** What a lease policy or rule gives, in a table cell. */
export function LeaseSummary({ lease }: { lease: Terms }) {
  return (
    <span className="text-secondary">
      {lease.default_days ? `${lease.default_days} days, up to ${lease.max_days ?? "no limit"}` : "None"}
      {lease.max_lifetime_days != null && (
        <span className="block text-muted">Lives {lease.max_lifetime_days} days at most</span>
      )}
      {lease.enforcement === "soft" && <span className="block text-muted">Soft: longer warns</span>}
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
        {days("default_days", "Lease, in days", "Empty for none")}
        {days("max_days", "At most, in days", "Empty for none")}
        <Field label="Extensions" htmlFor={`${id}-ext`} error={errors["/max_extensions"]}>
          <Input
            id={`${id}-ext`}
            type="number"
            min={0}
            value={f.max_extensions}
            onChange={(e) => setF({ ...f, max_extensions: Number(e.target.value) })}
          />
        </Field>
        <Field label="Grace, in days" htmlFor={`${id}-grace`} error={errors["/grace_days"]}>
          <Input
            id={`${id}-grace`}
            type="number"
            min={0}
            value={f.grace_days}
            onChange={(e) => setF({ ...f, grace_days: Number(e.target.value) })}
          />
        </Field>
      </div>
      {days(
        "max_lifetime_days",
        "Lifetime, in days",
        "How long a deployment lives in all, from its creation. An extension past it needs approval. Empty for no limit",
      )}
      <Field label="When it expires" htmlFor={`${id}-action`} error={errors["/expiry_action"]}>
        <Select
          id={`${id}-action`}
          value={f.expiry_action}
          onChange={(v) => setF({ ...f, expiry_action: v as S["LeaseExpiryAction"] })}
          options={Object.entries(expiryLabels).map(([value, label]) => ({ value, label }))}
        />
      </Field>
      <Field
        label="Enforcement"
        htmlFor={`${id}-enforcement`}
        error={errors["/enforcement"]}
        hint="Soft: a longer lease, one more extension or one past the lifetime warns the requester instead of being refused or approved."
      >
        <Select
          id={`${id}-enforcement`}
          value={f.enforcement ?? "hard"}
          onChange={(v) => setF({ ...f, enforcement: v as S["PolicyEnforcement"] })}
          options={enforcementOptions}
        />
      </Field>
    </>
  );
}

function LeaseSheet({ lease, onClose }: { lease?: Lease; onClose: () => void }) {
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
      toast.success("Lease policy saved");
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
      title={lease ? "Change the lease policy" : "New lease policy"}
      description="How long deployments live in an environment, for every project or one; a project's own policy wins."
      submitLabel={save.isPending ? "Saving…" : "Save lease policy"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="Environment" htmlFor="lease-env" error={errors["/environment"]}>
          <Select
            id="lease-env"
            value={f.environment}
            disabled={Boolean(lease)}
            onChange={(v) => setF({ ...f, environment: v })}
            options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
          />
        </Field>
        <Field
          label="Project"
          htmlFor="lease-project"
          error={errors["/project_id"]}
          hint="A project's own policy wins, but it can't be looser than every project's"
        >
          <Select
            id="lease-project"
            value={f.project_id ?? "all"}
            disabled={Boolean(lease)}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options("Every project")}
          />
        </Field>
      </div>
      <LeaseTermsFields id="lease" f={f} setF={setF} errors={errors} />
    </Sheet>
  );
}

export function LeasePolicies({ rights }: { rights: PolicyRights }) {
  const refresh = useRefreshPolicies("leases");
  const leases = useQuery(adm.leases());
  const projects = useProjects();
  const [editing, setEditing] = useState<Lease | "new" | null>(null);
  const [removing, setRemoving] = useState<Lease | null>(null);
  const where = (l: Lease) => `${l.environment}${l.project_id ? `, ${projects.name(l.project_id)}` : ""}`;
  const remove = useMutation({
    mutationFn: (l: Lease) =>
      call(
        api.DELETE("/admin/lease-policies", {
          params: { query: { environment: l.environment, project_id: l.project_id ?? "" } },
        }),
      ),
    onSuccess: (_, l) => {
      void refresh();
      toast.success(`The lease in ${where(l)} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const items = leases.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New lease policy
    </Button>
  );
  const columns: Column<Lease>[] = [
    {
      id: "where",
      header: "Where",
      sortValue: (l) => where(l),
      cell: (l) => (
        <>
          <span className="font-medium">{l.environment}</span>
          <div className="text-secondary text-muted">
            {l.project_id ? projects.name(l.project_id) : "Every project"}
          </div>
          <SetByAdmin policy={l} />
        </>
      ),
    },
    {
      id: "lease",
      header: "Lease",
      cell: (l) => <LeaseSummary lease={l} />,
    },
    {
      id: "extensions",
      header: "Extensions",
      align: "end",
      cell: (l) => <span className="tnum">{l.max_extensions}</span>,
    },
    {
      id: "expiry",
      header: "When it expires",
      cell: (l) => (
        <span className="text-secondary text-muted">
          {expiryLabels[l.expiry_action]}, {l.grace_days} days&apos; grace
        </span>
      ),
    },
  ];
  return (
    <Panel
      title="Lease policies"
      description={`How long deployments live in each environment, and what happens when the lease ends. A lease rule that matches comes first.${
        rights.global ? "" : " You change your projects' own, no looser than every project's."
      }`}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label="Lease policies"
        columns={columns}
        rows={items}
        rowKey={(l) => `${l.environment}/${l.project_id ?? ""}`}
        minWidth={680}
        loading={leases.isPending}
        error={leases.isError && <ErrorState error={leases.error} onRetry={() => void leases.refetch()} />}
        empty={
          <EmptyState
            icon={Timer}
            title="No lease policy"
            body="Deployments live until someone deletes them. Add a policy to give them a lease."
            action={rights.any && add}
          />
        }
        rowMenuLabel={(l) => `Actions for the lease in ${where(l)}`}
        rowMenu={
          rights.any
            ? (l) =>
                rights.changes(l) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(l)}>Change</MenuItem>
                    {l.project_id && (
                      <>
                        <MenuSeparator />
                        <MenuItem danger onSelect={() => setRemoving(l)}>
                          Remove
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
          title={`Remove the lease in ${where(removing)}?`}
          description={`${projects.name(removing.project_id ?? "")}'s own lease in ${removing.environment} stops applying.`}
          consequences={`New deployments in ${removing.environment} get the lease every project has there, and extensions follow it. Deployments keep the lease they have.`}
          confirmLabel="Remove lease policy"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
