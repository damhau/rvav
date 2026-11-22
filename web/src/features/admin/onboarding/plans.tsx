// Onboarding plans (X1.5): a filter of the inventory's VMs and where they
// go, run by hand or every few hours; each run adopts what no deployment
// has yet, and says what it skipped and why.
import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ListFilter } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
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
import { i18n } from "@/lib/i18n";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed, fieldErrors } from "../governance/common";
import { emptyTarget, inputProblems, planTarget, targetOf, TargetFields, useFocusRefused, type Target } from "./target";

type S = components["schemas"];
export type Plan = S["OnboardingPlan"];
type Run = S["OnboardingRun"];

const tx = i18n.getFixedT(null, "admin-onboarding");

const filterGroup = { "/filter": "obp-q" };
const tagExample = "app/erp";

export function filterText(f: S["OnboardingFilter"]): string {
  return (
    [
      f.q && tx("filter.q", { q: f.q }),
      f.folder && tx("filter.folder", { folder: f.folder }),
      f.tag && tx("filter.tag", { tag: f.tag }),
      f.cluster && tx("filter.cluster", { cluster: f.cluster }),
      f.vcenter && tx("filter.vcenter", { vcenter: f.vcenter }),
      f.power_state && tx(`filter.power.${f.power_state}`),
    ]
      .filter(Boolean)
      .join(", ") || tx("filter.every")
  );
}

export function targetText(p: Plan, projectName: (id: string) => string): string {
  const t = p.target;
  const values = { project: projectName(t.project_id), environment: t.environment ?? "", component: t.component };
  const as = t.component ? "As" : "";
  if (t.deployment_id)
    return tx(`plans.target.into${as}`, { ...values, name: p.deployment?.name ?? tx("plans.target.deleted") });
  if (t.deployment_name) return tx(`plans.target.one${as}`, { ...values, name: t.deployment_name });
  return tx(`plans.target.each${as}`, values);
}

export function scheduleText(p: Plan): string {
  if (!p.every_hours) return tx("plans.byHand");
  const every = tx("plans.every", { count: p.every_hours });
  return p.next_run_at ? tx("plans.everyNext", { every, when: relative(p.next_run_at) }) : every;
}

const statusTone: Record<Run["status"], "run" | "ok" | "warn" | "bad"> = {
  running: "run",
  succeeded: "ok",
  partial: "warn",
  failed: "bad",
};

export function RunStatus({ status }: { status: Run["status"] }) {
  const { t } = useTranslation("admin-onboarding");
  return <Badge tone={statusTone[status]}>{t(`run.status.${status}`)}</Badge>;
}

export const countsText = (r: Run) => tx("run.counts", { adopted: r.adopted, skipped: r.skipped, failed: r.failed });

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
      toast.success(tx("plans.runs", { name: r.plan_name }));
    },
    onError: (e) => failed(e, tx("plans.notRun")),
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
  const { t } = useTranslation("admin-onboarding");
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
      {!given ? t("sheet.noCriterion") : count.data ? t("sheet.matching", { count: count.data.total }) : ""}
    </p>
  );
}

export function PlanSheet({ plan, onClose }: { plan?: Plan; onClose: () => void }) {
  const { t } = useTranslation("admin-onboarding");
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
      toast.success(t("plans.saved", { name: p.name }));
      onClose();
      if (!plan) void navigate({ to: "/admin/onboarding/plans/$planId", params: { planId: p.id } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("plans.notSaved"));
    },
  });
  const set = (k: keyof Filter) => (v: string) => setFilter({ ...filter, [k]: v });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={plan ? t("sheet.change", { name: plan.name }) : t("plans.new")}
      description={t("sheet.lead")}
      className="w-[min(720px,100vw)]"
      submitLabel={save.isPending ? t("sheet.saving") : t("sheet.save")}
      onSubmit={() => {
        setErrors({});
        const problems = inputProblems(target);
        setLocal(problems);
        if (Object.keys(problems).length === 0) save.mutate();
      }}
    >
      <Field label={t("sheet.name")} htmlFor="obp-name" error={errors["/name"]}>
        <Input id="obp-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </Field>
      <Field label={t("sheet.description")} htmlFor="obp-description" error={errors["/description"]}>
        <Textarea
          id="obp-description"
          rows={2}
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Panel as="fieldset" title={t("sheet.which")} describedBy={errors["/filter"] ? "obp-filter-error" : undefined}>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label={t("sheet.q")} htmlFor="obp-q" hint={t("sheet.qHint")} error={errors["/filter/q"]}>
            <Input id="obp-q" value={filter.q} onChange={(e) => set("q")(e.target.value)} />
          </Field>
          <Field
            label={t("sheet.folder")}
            htmlFor="obp-folder"
            hint={t("sheet.folderHint")}
            error={errors["/filter/folder"]}
          >
            <Input
              id="obp-folder"
              className="font-mono text-secondary"
              placeholder="/GVA/vm/legacy"
              value={filter.folder}
              onChange={(e) => set("folder")(e.target.value)}
            />
          </Field>
          <Field label={t("sheet.tag")} htmlFor="obp-tag" hint={t("sheet.tagHint")} error={errors["/filter/tag"]}>
            <Input
              id="obp-tag"
              className="font-mono text-secondary"
              placeholder={tagExample}
              value={filter.tag}
              onChange={(e) => set("tag")(e.target.value)}
            />
          </Field>
          <Field label={t("sheet.cluster")} htmlFor="obp-cluster">
            <Input id="obp-cluster" value={filter.cluster} onChange={(e) => set("cluster")(e.target.value)} />
          </Field>
          <Field label="vCenter" htmlFor="obp-vcenter">
            <Select
              id="obp-vcenter"
              value={filter.vcenter || "any"}
              onChange={(v) => set("vcenter")(v === "any" ? "" : v)}
              options={[
                { value: "any", label: t("sheet.any") },
                ...(vcenters.data?.items ?? []).map((v) => ({ value: v.name, label: v.name })),
              ]}
            />
          </Field>
          <Field label={t("sheet.power")} htmlFor="obp-power" error={errors["/filter/power_state"]}>
            <Select
              id="obp-power"
              value={filter.power_state || "any"}
              onChange={(v) => set("power_state")(v === "any" ? "" : v)}
              options={[
                { value: "any", label: t("sheet.any") },
                { value: "on", label: t("sheet.on") },
                { value: "off", label: t("sheet.off") },
                { value: "suspended", label: t("sheet.suspended") },
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
      <Panel as="fieldset" title={t("target.where")}>
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
      <Field label={t("sheet.every")} htmlFor="obp-every" hint={t("sheet.everyHint")} error={errors["/every_hours"]}>
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
  const { t } = useTranslation("admin-onboarding");
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/onboarding/plans/{planId}", { params: { path: { planId: plan.id } } })),
    onSuccess: () => {
      invalidateOnboarding(qc);
      toast.success(t("plans.deleted", { name: plan.name }));
      onClose();
      onDeleted?.();
    },
    onError: (e) => failed(e, t("plans.notDeleted")),
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("delete.title", { name: plan.name })}
      description={t("delete.body")}
      consequences={t("delete.consequences")}
      confirmLabel={t("delete.confirm")}
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
  const { t } = useTranslation("admin-onboarding");
  const [deleting, setDeleting] = useState<Plan | null>(null);
  const runNow = useRunNow();
  const projectName = useProjectName();
  const columns: Column<Plan>[] = [
    {
      id: "name",
      header: t("plans.columns.plan"),
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
      header: t("plans.columns.vms"),
      cell: (p) => (
        <div className="text-secondary">
          {filterText(p.filter)}
          <div className="text-faint">{t("plans.toAdopt", { count: p.matching })}</div>
        </div>
      ),
    },
    {
      id: "target",
      header: t("target.where"),
      cell: (p) => <span className="text-secondary">{targetText(p, projectName)}</span>,
    },
    {
      id: "last",
      header: t("plans.columns.last"),
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
          <span className="text-secondary text-faint">{t("plans.neverRun")}</span>
        ),
    },
    {
      id: "when",
      header: t("plans.columns.runs"),
      cell: (p) => <span className="text-secondary text-muted">{scheduleText(p)}</span>,
    },
  ];
  return (
    <>
      <Panel title={t("plans.title")} flush>
        <DataTable
          label={t("plans.label")}
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
              title={t("plans.none")}
              body={t("plans.noneBody")}
              action={
                <Button variant="primary" onClick={() => setEditing("new")}>
                  {t("plans.new")}
                </Button>
              }
            />
          }
          rowMenuLabel={(p) => t("plans.actionsFor", { name: p.name })}
          rowMenu={(p) => (
            <>
              <MenuItem disabled={p.running || runNow.isPending} onSelect={() => runNow.mutate(p)}>
                <span>{t("plans.runNow")}</span> {p.running && <span className="text-faint">{t("plans.goesOn")}</span>}
              </MenuItem>
              <MenuItem onSelect={() => setEditing(p)}>{t("plans.change")}</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={p.running} onSelect={() => setDeleting(p)}>
                <span>{t("plans.delete")}</span> {p.running && <span className="text-faint">{t("plans.goesOn")}</span>}
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
