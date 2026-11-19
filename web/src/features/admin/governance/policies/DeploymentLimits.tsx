import { useMutation, useQuery } from "@tanstack/react-query";
import { Gauge } from "lucide-react";
import { useRef, useState } from "react";
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
    return [`${name}: ${v}${unit ? ` ${unit}` : ""}`];
  });
}

const kindNames: Record<Kind, string> = {
  deployments_per_user: "deployments per user",
  deployments_per_project: "deployments per project",
  vcpu_per_deployment: "vCPU per deployment",
  memory_gb_per_deployment: "memory per deployment",
};

const joined = (n: string[]) => (n.length < 2 ? (n[0] ?? "") : `${n.slice(0, -1).join(", ")} and ${n.at(-1)}`);

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
      title={policy ? `Change ${policy.name}` : "New deployment limit policy"}
      description="How many deployments each user or the project may have, and how large each may be. Leave a limit empty to leave it to other policies."
      className="w-[min(640px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save policy"}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <Field label="Name" htmlFor="dl-name" error={errors["/name"]}>
          <Input id="dl-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label="Priority" htmlFor="dl-priority" hint="Lower first">
          <Input id="dl-priority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Environment" htmlFor="dl-env" error={errors["/environment"]}>
          <Select
            id="dl-env"
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
          htmlFor="dl-project"
          error={errors["/project_id"]}
          hint="A project's policy only tightens a hard global one"
        >
          <Select
            id="dl-project"
            value={f.project_id ?? "all"}
            onChange={(v) => setF({ ...f, project_id: v === "all" ? null : v })}
            options={projects.options("Every project")}
          />
        </Field>
      </div>
      <Field label="Enforcement" htmlFor="dl-enforcement">
        <Select
          id="dl-enforcement"
          value={f.enforcement ?? "hard"}
          onChange={(v) => setF({ ...f, enforcement: v as S["PolicyEnforcement"] })}
          options={enforcementOptions}
        />
      </Field>
      <fieldset className="flex flex-col gap-2">
        <legend className="label mb-1">Limits</legend>
        <div className="grid grid-cols-2 gap-3">
          {limitKinds.map((k) => {
            const [name, unit] = limitLabel[k];
            return (
              <Field key={k} label={unit ? `${name} (${unit})` : name} htmlFor={`dl-${k}`} error={errors[`/${k}`]}>
                <Input
                  id={`dl-${k}`}
                  type="number"
                  placeholder="No limit"
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
        Enabled
      </label>
    </Sheet>
  );
}

/** Deployment limit policies (ADR-0047): global ones and those of the caller's projects. */
export function DeploymentLimits({ rights }: { rights: PolicyRights }) {
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
      toast.success(`${p.name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const turn = useMutation({
    mutationFn: (p: Policy) => put({ ...p, enabled: !p.enabled }),
    onSuccess: (_, p) => toast.success(`${p.name} turned ${p.enabled ? "off" : "on"}`),
    onSettled: refresh,
    onError: (e) => failed(e, "Not saved"),
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
      New deployment limit policy
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
            {p.environment ? envName(p.environment) : "Every environment"},{" "}
            {p.project_id ? projects.name(p.project_id) : "every project"}
          </div>
          <SetByAdmin policy={p} />
        </>
      ),
    },
    {
      id: "limits",
      header: "Limits",
      cell: (p) => (
        <ul className="flex flex-col text-secondary">
          {limitsText(p).map((t) => (
            <li key={t}>{t}</li>
          ))}
        </ul>
      ),
    },
    {
      id: "enforcement",
      header: "Enforcement",
      cell: (p) => (p.enforcement === "soft" ? "Soft: warns" : "Hard: refuses"),
    },
    { id: "priority", header: "Priority", align: "end", cell: (p) => <span className="tnum">{p.priority}</span> },
  ];
  return (
    <Panel
      title="Deployment limits"
      description={`How many deployments each user or each project may have, and how much vCPU and memory each deployment may take. Each limit is decided on its own: the first policy by priority that sets it decides, and every hard global policy that sets it holds too.${
        rights.global || !rights.any ? "" : " You change your projects' own."
      }`}
      flush
      actions={rights.any && Boolean(items?.length) && add}
    >
      <DataTable
        label="Deployment limit policies"
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={760}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Gauge}
            title="No deployment limit"
            body={`Users and projects may have as many deployments, each as large, as their quotas allow.${rights.any ? " Add a policy to limit them." : ""}`}
            action={rights.any && add}
          />
        }
        rowMenuLabel={(p) => `Actions for ${p.name}`}
        rowMenu={
          rights.any
            ? (p) =>
                rights.changes(p) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(p)}>Change</MenuItem>
                    <MenuItem disabled={turn.isPending} onSelect={() => turn.mutate(p)}>
                      {p.enabled ? "Turn off" : "Turn on"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(p)}>
                      Remove
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
          title={`Remove ${removing.name}?`}
          description={`It stops limiting ${joined(limitKinds.filter((k) => removing[k] != null).map((k) => kindNames[k]))}.`}
          consequences="New requests, updates and resizes meet the next policy that sets each limit, or none. Deployments that already pass a limit stay as they are."
          confirmLabel="Remove policy"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
