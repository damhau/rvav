import { useMutation, useQuery } from "@tanstack/react-query";
import { ListFilter } from "lucide-react";
import { useRef, useState } from "react";
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
      returnFocus={returnFocus}
      title={rule ? `Change ${rule.name}` : "New lease rule"}
      description="A lease for the deployments its condition matches. Rules are tried by priority before the lease policies, and the first that matches decides."
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save lease rule"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label="Name" htmlFor="lr-name" error={errors["/name"]}>
          <Input id="lr-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label="Priority" htmlFor="lr-priority" hint="Lower first">
          <Input id="lr-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <Field
        label="Applies when (CEL)"
        htmlFor="lr-condition"
        error={errors["/condition"]}
        hint='Reads what approval conditions read, for example item.slug == "gpu-server" or inputs.size == "S"'
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
        <Field label="Environment" htmlFor="lr-env" error={errors["/environment"]}>
          <Select
            id="lr-env"
            value={f.environment ?? "all"}
            onChange={(v) => setF({ ...f, environment: v === "all" ? null : v })}
            options={[
              { value: "all", label: "Every environment" },
              ...(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name })),
            ]}
          />
        </Field>
        <Field
          label="Project"
          htmlFor="lr-project"
          error={errors["/project_id"]}
          hint="A project admin's rule gives no more than what administrators set"
        >
          <Select
            id="lr-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options("Every project")}
          />
        </Field>
      </div>
      <LeaseTermsFields id="lr" f={f} setF={setF} errors={errors} />
    </Sheet>
  );
}

export function LeaseRules({ rights }: { rights: PolicyRights }) {
  const refresh = useRefreshPolicies("lease-rules");
  const rules = useQuery(adm.leaseRules());
  const envs = useQuery(q.environments());
  const projects = useProjects();
  const [editing, setEditing] = useState<Rule | "new" | null>(null);
  const [fromEmpty, setFromEmpty] = useState(false);
  const addRef = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<Rule | null>(null);
  const envName = (key: string) => envs.data?.items.find((e) => e.key === key)?.name ?? key;
  const remove = useMutation({
    mutationFn: (r: Rule) => call(api.DELETE("/admin/lease-rules/{id}", { params: { path: { id: r.id } } })),
    onSuccess: (_, r) => {
      void refresh();
      toast.success(`${r.name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
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
      New lease rule
    </Button>
  );
  const columns: Column<Rule>[] = [
    {
      id: "rule",
      header: "Rule",
      cell: (r) => (
        <>
          <span className="font-medium">{r.name}</span>
          <div className="text-secondary text-muted">
            {r.environment ? envName(r.environment) : "Every environment"},{" "}
            {r.project_id ? projects.name(r.project_id) : "every project"}
          </div>
          <SetByAdmin policy={r} />
        </>
      ),
    },
    {
      id: "condition",
      header: "Applies when",
      cell: (r) => <span className="font-mono text-secondary">{r.condition}</span>,
    },
    { id: "lease", header: "Lease", cell: (r) => <LeaseSummary lease={r} /> },
    {
      id: "extensions",
      header: "Extensions",
      align: "end",
      cell: (r) => <span className="tnum">{r.max_extensions}</span>,
    },
    { id: "priority", header: "Priority", align: "end", cell: (r) => <span className="tnum">{r.priority}</span> },
  ];
  return (
    <Panel
      title="Lease rules"
      description={`A lease for the deployments a condition matches, such as an item or a size. Rules are tried by priority before the lease policies, and the first that matches decides.${
        rights.global || !rights.any
          ? ""
          : " You change your projects' own, which give no more than what administrators set."
      }`}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label="Lease rules"
        columns={columns}
        rows={items}
        rowKey={(r) => r.id}
        minWidth={760}
        loading={rules.isPending}
        error={rules.isError && <ErrorState error={rules.error} onRetry={() => void rules.refetch()} />}
        empty={
          <EmptyState
            icon={ListFilter}
            title="No lease rule"
            body={`Every deployment gets its environment's lease, or its project's.${rights.any ? " Add a rule to give some of them another one." : ""}`}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(r) => `Actions for ${r.name}`}
        rowMenu={
          rights.any
            ? (r) =>
                rights.changes(r) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(r)}>Change</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(r)}>
                      Remove
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
          title={`Remove ${removing.name}?`}
          description={
            <>
              It stops giving its lease to the deployments where <span className="font-mono">{removing.condition}</span>
              .
            </>
          }
          consequences={
            removing.project_id && !removing.set_by_admin
              ? "New requests get the next rule that matches them, or the lease policies. Deployments it decided follow the administrators' rule it was held to from now on, or else their project's or environment's lease."
              : "New requests get the next rule that matches them, or the lease policies. Deployments it decided follow their project's or environment's lease from now on."
          }
          confirmLabel="Remove lease rule"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
