// Onboarding plans (X1.5): a filter of the inventory's VMs and where they
// go, run by hand or every few hours; each run adopts what no deployment
// has yet, and says what it skipped and why.
import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ListFilter } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Badge } from "@/components/ui/badge";
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
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed, fieldErrors } from "../governance/common";
import { emptyTarget, inputProblems, planTarget, targetOf, TargetFields, useFocusRefused, type Target } from "./target";

type S = components["schemas"];
export type Plan = S["OnboardingPlan"];
type Run = S["OnboardingRun"];

const vmsText = (n: number) => `${n} VM${n === 1 ? "" : "s"}`;
const filterGroup = { "/filter": "obp-q" };

export function filterText(f: S["OnboardingFilter"]): string {
  const power = { on: "powered on", off: "powered off", suspended: "suspended" } as const;
  return (
    [
      f.q && `name or address ${f.q}`,
      f.folder && `in ${f.folder}`,
      f.tag && `tagged ${f.tag}`,
      f.cluster && `on ${f.cluster}`,
      f.vcenter && `in ${f.vcenter}`,
      f.power_state && power[f.power_state],
    ]
      .filter(Boolean)
      .join(", ") || "Every VM"
  );
}

export function targetText(p: Plan, projectName: (id: string) => string): string {
  const t = p.target;
  const where = projectName(t.project_id);
  const component = t.component ? `, as ${t.component} VMs` : "";
  if (t.deployment_id) return `Into ${p.deployment?.name ?? "a deployment since deleted"}${component}`;
  if (t.deployment_name)
    return `Into ${t.deployment_name}, one deployment in ${where} ${t.environment ?? ""}${component}`;
  return `Each into a deployment of its own in ${where} ${t.environment ?? ""}${component}`;
}

export function scheduleText(p: Plan): string {
  if (!p.every_hours) return "When run by hand";
  const every = p.every_hours === 1 ? "Every hour" : `Every ${p.every_hours} hours`;
  return p.next_run_at ? `${every}, next run ${relative(p.next_run_at)}` : every;
}

const statusTone: Record<Run["status"], "run" | "ok" | "warn" | "bad"> = {
  running: "run",
  succeeded: "ok",
  partial: "warn",
  failed: "bad",
};
const statusLabel: Record<Run["status"], string> = {
  running: "Running",
  succeeded: "Succeeded",
  partial: "Some failed",
  failed: "Failed",
};

export function RunStatus({ status }: { status: Run["status"] }) {
  return <Badge tone={statusTone[status]}>{statusLabel[status]}</Badge>;
}

export const countsText = (r: Run) => `${r.adopted} adopted, ${r.skipped} skipped, ${r.failed} failed`;

export function useProjectName() {
  const projects = useQuery(adm.projectNames()).data?.items;
  return (id: string) => projects?.find((p) => p.id === id)?.name ?? id;
}

export const invalidateOnboarding = (qc: QueryClient) => {
  void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
};

export function useRunNow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (p: Plan) =>
      call(api.POST("/admin/onboarding/plans/{planId}/runs", { params: { path: { planId: p.id } } })),
    onSuccess: (r) => {
      invalidateOnboarding(qc);
      toast.success(`${r.plan_name} runs`);
    },
    onError: (e) => failed(e, "Not run"),
  });
}

type Filter = Required<{ [K in keyof S["OnboardingFilter"]]: string }>;
const emptyFilter: Filter = { q: "", vcenter: "", cluster: "", power_state: "", folder: "", tag: "" };

function filterOf(f: Filter): S["OnboardingFilter"] {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(f)) if (v.trim() !== "") out[k] = v.trim();
  return out;
}

/** How many VMs the filter matches now, said as it changes. */
function Matching({ filter }: { filter: S["OnboardingFilter"] }) {
  const key = useDebounced(JSON.stringify(filter), 300);
  const given = key !== "{}";
  const count = useQuery({
    queryKey: ["admin", "onboarding", "count", key],
    queryFn: ({ signal }) =>
      call(
        api.GET("/admin/onboarding/vms", {
          params: { query: { ...(JSON.parse(key) as S["OnboardingFilter"]), limit: 1 } },
          signal,
        }),
      ),
    enabled: given,
    placeholderData: keepPreviousData,
  });
  return (
    <p className="text-secondary text-muted" aria-live="polite">
      {!given
        ? "Give a criterion at least: a plan never adopts every VM rvav doesn't manage."
        : count.data
          ? `${vmsText(count.data.total)} rvav doesn't manage match now.`
          : ""}
    </p>
  );
}

export function PlanSheet({ plan, onClose }: { plan?: Plan; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const vcenters = useQuery(adm.vcenters());
  const [f, setF] = useState({
    name: plan?.name ?? "",
    description: plan?.description ?? "",
    every: plan?.every_hours ? String(plan.every_hours) : "",
  });
  const [filter, setFilter] = useState<Filter>({ ...emptyFilter, ...plan?.filter });
  const [target, setTarget] = useState<Target>(plan ? targetOf(plan.target) : emptyTarget);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [local, setLocal] = useState<Record<number, string>>({});
  useFocusRefused(errors, filterGroup);
  const save = useMutation({
    mutationFn: () => {
      const body: S["OnboardingPlanWrite"] = {
        name: f.name,
        description: f.description,
        filter: filterOf(filter),
        target: planTarget(target),
        every_hours: f.every.trim() === "" ? undefined : Number(f.every),
      };
      return plan
        ? call(api.PUT("/admin/onboarding/plans/{planId}", { params: { path: { planId: plan.id } }, body }))
        : call(api.POST("/admin/onboarding/plans", { body }));
    },
    onSuccess: (p) => {
      invalidateOnboarding(qc);
      toast.success(`${p.name} saved`);
      onClose();
      if (!plan) void navigate({ to: "/admin/onboarding/plans/$planId", params: { planId: p.id } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const set = (k: keyof Filter) => (v: string) => setFilter({ ...filter, [k]: v });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={plan ? `Change ${plan.name}` : "New plan"}
      description="Which VMs it adopts, where they go, and when it runs. Each run adopts the VMs it matches that no deployment has."
      className="w-[min(720px,100vw)]"
      submitLabel={save.isPending ? "Saving…" : "Save plan"}
      onSubmit={() => {
        setErrors({});
        const problems = inputProblems(target);
        setLocal(problems);
        if (Object.keys(problems).length === 0) save.mutate();
      }}
    >
      <Field label="Name" htmlFor="obp-name" error={errors["/name"]}>
        <Input id="obp-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </Field>
      <Field label="Description" htmlFor="obp-description" error={errors["/description"]}>
        <Textarea
          id="obp-description"
          rows={2}
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Panel as="fieldset" title="Which VMs" describedBy={errors["/filter"] ? "obp-filter-error" : undefined}>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field
            label="Name or address"
            htmlFor="obp-q"
            hint="Part of the name or host name, or the address's start"
            error={errors["/filter/q"]}
          >
            <Input id="obp-q" value={filter.q} onChange={(e) => set("q")(e.target.value)} />
          </Field>
          <Field label="Folder" htmlFor="obp-folder" hint="And the folders under it" error={errors["/filter/folder"]}>
            <Input
              id="obp-folder"
              className="font-mono text-secondary"
              placeholder="/GVA/vm/legacy"
              value={filter.folder}
              onChange={(e) => set("folder")(e.target.value)}
            />
          </Field>
          <Field label="Tag" htmlFor="obp-tag" hint="A vSphere tag, as category/tag" error={errors["/filter/tag"]}>
            <Input
              id="obp-tag"
              className="font-mono text-secondary"
              placeholder="app/erp"
              value={filter.tag}
              onChange={(e) => set("tag")(e.target.value)}
            />
          </Field>
          <Field label="Cluster" htmlFor="obp-cluster">
            <Input id="obp-cluster" value={filter.cluster} onChange={(e) => set("cluster")(e.target.value)} />
          </Field>
          <Field label="vCenter" htmlFor="obp-vcenter">
            <Select
              id="obp-vcenter"
              value={filter.vcenter || "any"}
              onChange={(v) => set("vcenter")(v === "any" ? "" : v)}
              options={[
                { value: "any", label: "Any" },
                ...(vcenters.data?.items ?? []).map((v) => ({ value: v.name, label: v.name })),
              ]}
            />
          </Field>
          <Field label="Power" htmlFor="obp-power" error={errors["/filter/power_state"]}>
            <Select
              id="obp-power"
              value={filter.power_state || "any"}
              onChange={(v) => set("power_state")(v === "any" ? "" : v)}
              options={[
                { value: "any", label: "Any" },
                { value: "on", label: "On" },
                { value: "off", label: "Off" },
                { value: "suspended", label: "Suspended" },
              ]}
            />
          </Field>
        </div>
        {errors["/filter"] && (
          <p id="obp-filter-error" role="alert" className="mt-3 text-secondary text-bad">
            {errors["/filter"]}
          </p>
        )}
        <Matching filter={filterOf(filter)} />
      </Panel>
      <Panel as="fieldset" title="Where they go">
        <TargetFields
          t={target}
          onChange={(next) => {
            // A row gone: the problems found by row no longer line up.
            if (next.inputs.length < target.inputs.length) setLocal({});
            setTarget(next);
          }}
          errors={errors}
          at="/target"
          id="obp"
          local={local}
          current={plan?.deployment}
        />
      </Panel>
      <Field
        label="Run every (hours)"
        htmlFor="obp-every"
        hint="From 1 to 168, a week. Empty: only when you run it."
        error={errors["/every_hours"]}
      >
        <Input
          id="obp-every"
          type="number"
          min={1}
          max={168}
          className="w-32"
          value={f.every}
          onChange={(e) => setF({ ...f, every: e.target.value })}
        />
      </Field>
    </Sheet>
  );
}

export function DeletePlan({ plan, onClose, onDeleted }: { plan: Plan; onClose: () => void; onDeleted?: () => void }) {
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/onboarding/plans/{planId}", { params: { path: { planId: plan.id } } })),
    onSuccess: () => {
      invalidateOnboarding(qc);
      toast.success(`${plan.name} deleted`);
      onClose();
      onDeleted?.();
    },
    onError: (e) => failed(e, "Not deleted"),
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Delete ${plan.name}?`}
      description="Its runs and what they recorded go with it."
      consequences="The deployments it adopted VMs into stay, with their VMs."
      confirmLabel="Delete plan"
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
    />
  );
}

export function PlansTab({
  editing,
  setEditing,
}: {
  editing: Plan | "new" | null;
  setEditing: (p: Plan | "new" | null) => void;
}) {
  const plans = useQuery({
    ...adm.onboardingPlans(),
    refetchInterval: (q) => (q.state.data?.items.some((p) => p.running) ? 1500 : false),
  });
  const [deleting, setDeleting] = useState<Plan | null>(null);
  const runNow = useRunNow();
  const projectName = useProjectName();
  const columns: Column<Plan>[] = [
    {
      id: "name",
      header: "Plan",
      sortValue: (p) => p.name,
      cell: (p) => (
        <>
          <Link
            to="/admin/onboarding/plans/$planId"
            params={{ planId: p.id }}
            className="font-medium text-fg hover:text-accent"
          >
            {p.name}
          </Link>
          {p.description && <div className="text-secondary text-faint">{p.description}</div>}
        </>
      ),
    },
    {
      id: "vms",
      header: "VMs it adopts",
      cell: (p) => (
        <div className="text-secondary">
          {filterText(p.filter)}
          <div className="text-faint">{vmsText(p.matching)} to adopt now</div>
        </div>
      ),
    },
    {
      id: "target",
      header: "Where they go",
      cell: (p) => <span className="text-secondary">{targetText(p, projectName)}</span>,
    },
    {
      id: "last",
      header: "Last run",
      sortValue: (p) => p.last_run?.started_at,
      cell: (p) =>
        p.last_run ? (
          <div className="text-secondary">
            <span className="flex flex-wrap items-center gap-2">
              <RunStatus status={p.last_run.status} />
              <span className="text-muted" title={fullDate(p.last_run.started_at)}>
                {relative(p.last_run.started_at)}
              </span>
            </span>
            <div className="text-faint">{countsText(p.last_run)}</div>
          </div>
        ) : (
          <span className="text-secondary text-faint">Never run</span>
        ),
    },
    { id: "when", header: "Runs", cell: (p) => <span className="text-secondary text-muted">{scheduleText(p)}</span> },
  ];
  return (
    <>
      <Panel title="Plans" flush>
        <DataTable
          label="Onboarding plans"
          columns={columns}
          rows={plans.data?.items}
          rowKey={(p) => p.id}
          rowLinks
          minWidth={900}
          loading={plans.isPending}
          error={plans.isError && <ErrorState error={plans.error} onRetry={() => void plans.refetch()} />}
          empty={
            <EmptyState
              icon={ListFilter}
              title="No plan yet"
              body="A plan adopts the VMs its filter matches, now and as they appear in vCenter: when you run it, or every few hours."
              action={
                <Button variant="primary" onClick={() => setEditing("new")}>
                  New plan
                </Button>
              }
            />
          }
          rowMenuLabel={(p) => `Actions for ${p.name}`}
          rowMenu={(p) => (
            <>
              <MenuItem disabled={p.running || runNow.isPending} onSelect={() => runNow.mutate(p)}>
                <span>Run now</span> {p.running && <span className="text-faint">a run goes on</span>}
              </MenuItem>
              <MenuItem onSelect={() => setEditing(p)}>Change</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={p.running} onSelect={() => setDeleting(p)}>
                <span>Delete</span> {p.running && <span className="text-faint">a run goes on</span>}
              </MenuItem>
            </>
          )}
        />
      </Panel>
      {editing && <PlanSheet plan={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {deleting && <DeletePlan plan={deleting} onClose={() => setDeleting(null)} />}
    </>
  );
}
