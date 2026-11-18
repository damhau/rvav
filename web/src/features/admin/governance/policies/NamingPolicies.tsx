import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Tag } from "lucide-react";
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
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, OfferEditor, offersText, writable } from "../common";
import { useProjects } from "./shared";

type S = components["schemas"];
type Policy = S["NamingPolicy"];

/** How a policy counts: "From 1, by 1" and whose counter it is. */
function counterText(p: Pick<Policy, "start" | "increment" | "counter">): string {
  return `From ${p.start}, by ${p.increment}${p.counter === "policy" ? ", one for the policy" : ""}`;
}

function NamingSheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useProjects();
  const [f, setF] = useState<S["NamingPolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : {
          name: "",
          template: "{project}-{environment}-{role}{seq:03}",
          counter: "prefix",
          start: 1,
          increment: 1,
          offers: [],
        },
  );
  const [sample, setSample] = useState({ project: "", environment: "dev", role: "app" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const preview = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/naming-preview", {
          body: {
            template: f.template,
            counter: f.counter,
            start: f.start,
            increment: f.increment,
            policy_id: policy?.id,
            project_id: sample.project || projects.items[0]?.id || "",
            environment: sample.environment,
            inputs: { role: sample.role },
            count: 3,
          },
        }),
      ),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "No preview");
    },
  });
  const save = useMutation({
    mutationFn: () =>
      policy
        ? call(api.PUT("/admin/naming-policies/{id}", { params: { path: { id: policy.id } }, body: f }))
        : call(api.POST("/admin/naming-policies", { body: f })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "naming"] });
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
      title={policy ? `Change ${policy.name}` : "New naming policy"}
      description="It names the VMs of the items, projects and environments it is offered to."
      submitLabel="Save naming policy"
      submitDisabled={save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label="Name" htmlFor="nm-name" error={errors["/name"]}>
        <Input id="nm-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </Field>
      <Field
        label="Template"
        htmlFor="nm-template"
        error={errors["/template"]}
        hint="{project}, {environment}, {role} or {inputs.x}, ending with {seq:03}"
      >
        <Input
          id="nm-template"
          className="font-mono text-secondary"
          value={f.template}
          onChange={(e) => setF({ ...f, template: e.target.value })}
        />
      </Field>
      <Field label="Counter" htmlFor="nm-counter">
        <Select
          id="nm-counter"
          value={f.counter}
          onChange={(v) => setF({ ...f, counter: v as Policy["counter"] })}
          options={[
            { value: "prefix", label: "One per name prefix (web-prd-app, web-prd-db…)" },
            { value: "policy", label: "One for the whole policy" },
          ]}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field
          label="Start at"
          htmlFor="nm-start"
          error={errors["/start"]}
          hint="A counter in use goes on from its last number"
        >
          <Input
            id="nm-start"
            type="number"
            min={0}
            value={f.start ?? 1}
            onChange={(e) => setF({ ...f, start: e.target.valueAsNumber })}
          />
        </Field>
        <Field label="Increment" htmlFor="nm-increment" error={errors["/increment"]}>
          <Input
            id="nm-increment"
            type="number"
            min={1}
            max={1000}
            value={f.increment ?? 1}
            onChange={(e) => setF({ ...f, increment: e.target.valueAsNumber })}
          />
        </Field>
      </div>
      <OfferEditor
        value={f.offers ?? []}
        onChange={(offers) => setF({ ...f, offers })}
        error={errorAt(errors, "/offers")}
      />
      <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">Next names</legend>
        <div className="grid grid-cols-3 gap-2">
          <Select
            aria-label="Preview project"
            value={sample.project || projects.items[0]?.id || ""}
            onChange={(v) => setSample({ ...sample, project: v })}
            options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
          />
          <Input
            aria-label="Preview environment"
            value={sample.environment}
            onChange={(e) => setSample({ ...sample, environment: e.target.value })}
          />
          <Input
            aria-label="Preview role"
            value={sample.role}
            onChange={(e) => setSample({ ...sample, role: e.target.value })}
          />
        </div>
        <div>
          <Button type="button" size="sm" disabled={preview.isPending} onClick={() => preview.mutate()}>
            Preview names
          </Button>
        </div>
        {preview.data && (
          <div className="text-secondary" aria-live="polite">
            {preview.data.problem && <p className="text-warn">{preview.data.problem}</p>}
            {preview.data.names.length > 0 && (
              <p>
                Next: <span className="font-mono">{preview.data.names.join(", ")}</span>
              </p>
            )}
            {preview.data.skipped.map((s) => (
              <p key={s.name} className="text-muted">
                Skips <span className="font-mono">{s.name}</span>: taken in {s.taken_in}
              </p>
            ))}
          </div>
        )}
      </fieldset>
    </Sheet>
  );
}

export function NamingPolicies({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const policies = useQuery(adm.naming());
  const projects = useProjects();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const remove = useMutation({
    mutationFn: (p: Policy) => call(api.DELETE("/admin/naming-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void qc.invalidateQueries({ queryKey: ["admin", "naming"] });
      toast.success(`${p.name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const items = policies.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New naming policy
    </Button>
  );
  const columns: Column<Policy>[] = [
    {
      id: "policy",
      header: "Policy",
      sortValue: (p) => p.name,
      cell: (p) => <span className="font-medium">{p.name}</span>,
    },
    {
      id: "template",
      header: "Template",
      cell: (p) => <span className="font-mono text-secondary">{p.template}</span>,
    },
    {
      id: "counter",
      header: "Counter",
      cell: (p) => <span className="text-secondary">{counterText(p)}</span>,
    },
    {
      id: "offers",
      header: "Offered to",
      cell: (p) => <span className="text-secondary text-muted">{offersText(p.offers, projects.name)}</span>,
    },
  ];
  return (
    <Panel
      title="Naming policies"
      description="The most specific offer wins over a blueprint's own template. A name is never given twice: rvav checks vCenter, DNS and every deployment it made."
      flush
      actions={canEdit && items?.length !== 0 && add}
    >
      <DataTable
        label="Naming policies"
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={640}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Tag}
            title="No naming policy"
            body="Each blueprint's own template names its VMs. Add a policy to name them the same way everywhere."
            action={canEdit && add}
          />
        }
        rowMenuLabel={(p) => `Actions for ${p.name}`}
        rowMenu={
          canEdit
            ? (p) => (
                <>
                  <MenuItem onSelect={() => setEditing(p)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(p)}>
                    Remove
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && <NamingSheet policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description={
            <>
              New VMs stop being named <span className="font-mono">{removing.template}</span>.
            </>
          }
          consequences={`Offered to ${offersText(removing.offers, projects.name)}. They get the next policy that is offered to them, or their blueprint's own template. VMs keep the names they have.`}
          confirmLabel="Remove naming policy"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
