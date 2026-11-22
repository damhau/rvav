import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { Workflow, Wrench } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { AwxAction, AwxActionUpdate, Day2FormSummary } from "@/lib/api/types";
import { adm, keys, q } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed, fieldErrors } from "./governance/common";

type Target = AwxAction["types"][number];

const targetKey = { "vsphere.vm": "vm", deployment: "deployment" } as const satisfies Record<Target, string>;
const targetLabel = (t: TFunction<"admin">, x: Target) => t(`day2.target.${targetKey[x]}`);
const celExample = "resource.os_family == 'linux'";

function Status({ a }: { a: AwxAction }) {
  const { t } = useTranslation("admin");
  if (a.gone) return <Badge tone="neutral">{t("day2.notInAwx")}</Badge>;
  if (!a.enabled) return <Badge tone="neutral">{t("day2.off")}</Badge>;
  if (a.warnings.length > 0) return <Badge tone="warn">{t("day2.checkSettings")}</Badge>;
  return <Badge tone="ok">{t("day2.offered")}</Badge>;
}

type WorkflowRegistration = components["schemas"]["WorkflowRegistration"];
type Forms = Map<string, Day2FormSummary>;

/** Which form an action asks for: its designed one once turned on, else its survey's or schema's. */
function FormState({ f, base }: { f?: Day2FormSummary; base: string }) {
  const { t } = useTranslation("admin");
  if (f?.active)
    return (
      <div className="text-secondary">
        <div>{t("day2.designed")}</div>
        <div className="text-caption text-faint" title={fullDate(f.updated_at)}>
          {t("day2.saved", { when: relative(f.updated_at) })}
        </div>
      </div>
    );
  return (
    <div className="text-secondary">
      <div>{base}</div>
      {f && <div className="text-caption text-faint">{t("day2.draft")}</div>}
    </div>
  );
}

function useForms() {
  const forms = useQuery({
    queryKey: keys.day2Forms,
    queryFn: ({ signal }) => call(api.GET("/admin/day2-forms", { signal })),
  });
  return new Map((forms.data?.items ?? []).map((f) => [f.action, f]));
}

function usePatch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ a, body }: { a: AwxAction; body: AwxActionUpdate; done: string }) =>
      call(api.PATCH("/admin/awx/actions/{key}", { params: { path: { key: a.key } }, body })),
    onSuccess: (saved, { done }) => {
      qc.setQueryData(keys.awxActions, (old: { items: AwxAction[] } | undefined) =>
        old ? { items: old.items.map((x) => (x.key === saved.key ? saved : x)) } : old,
      );
      toast.success(done);
    },
  });
}

function ActionSheet({ a, onClose }: { a: AwxAction; onClose: () => void }) {
  const { t } = useTranslation("admin");
  const patch = usePatch();
  const [name, setName] = useState(a.name);
  const [types, setTypes] = useState<Target[]>(a.types);
  const [condition, setCondition] = useState(a.condition);
  const [enabled, setEnabled] = useState(a.enabled);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const toggle = (x: Target, on: boolean) => setTypes((cur) => (on ? [...cur, x] : cur.filter((y) => y !== x)));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={patch.isPending}
      title={t("day2.change", { name: a.name })}
      description={
        <Trans
          t={t}
          i18nKey={a.workflow ? "day2.awxWorkflow" : "day2.awxJobTemplate"}
          values={{ template: a.template }}
          components={{ template: <span className="font-mono" /> }}
        />
      }
      submitLabel={patch.isPending ? t("day2.saving") : t("day2.save")}
      submitDisabled={types.length === 0 || name.trim() === ""}
      onSubmit={() => {
        setErrors({});
        patch.mutate(
          { a, body: { name, types, condition, enabled }, done: t("day2.savedToast", { name }) },
          {
            onSuccess: onClose,
            onError: (e) => {
              setErrors(fieldErrors(e));
              failed(e, t("day2.notSaved"));
            },
          },
        );
      }}
    >
      <Field
        label={t("day2.name")}
        htmlFor="awx-action-name"
        hint={name.trim() === "" ? t("day2.nameNeeded") : t("day2.nameHint")}
        error={errors["/name"]}
      >
        <Input id="awx-action-name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
      </Field>
      <fieldset
        className="flex flex-col gap-1.5"
        aria-describedby={errors["/types"] ? "awx-action-types-error" : undefined}
      >
        <legend className="mb-1 text-secondary font-medium">{t("day2.runsOn")}</legend>
        {(Object.keys(targetKey) as Target[]).map((x) => (
          <div key={x} className="flex items-center gap-2 text-body">
            <Checkbox id={`awx-action-${x}`} checked={types.includes(x)} onChange={(on) => toggle(x, on)} />
            <label htmlFor={`awx-action-${x}`}>{targetLabel(t, x)}</label>
            <span className="text-secondary text-faint">
              {x === "deployment" ? t("day2.everyVm") : t("day2.oneVm")}
            </span>
          </div>
        ))}
        {types.length === 0 && <p className="text-secondary text-muted">{t("day2.chooseWhere")}</p>}
        {errors["/types"] && (
          <p id="awx-action-types-error" className="text-secondary text-bad">
            {errors["/types"]}
          </p>
        )}
      </fieldset>
      <Field
        label={t("day2.condition")}
        htmlFor="awx-action-condition"
        error={errors["/condition"]}
        hint={
          <Trans
            t={t}
            i18nKey="day2.conditionHint"
            values={{ example: celExample }}
            components={{ cel: <span className="font-mono" /> }}
          />
        }
      >
        <Textarea
          id="awx-action-condition"
          className="font-mono text-secondary"
          value={condition}
          onChange={(e) => setCondition(e.target.value)}
        />
      </Field>
      <label htmlFor="awx-action-enabled" className="flex items-center gap-2 text-body">
        <Checkbox id="awx-action-enabled" checked={enabled} onChange={setEnabled} />
        {t("day2.offeredOnDeployments")}
      </label>
    </Sheet>
  );
}

const columnsOf = (t: TFunction<"admin">, forms: Forms): Column<AwxAction>[] => [
  {
    id: "action",
    header: t("day2.action"),
    sortValue: (a) => a.name,
    cell: (a) => (
      <>
        <div className="font-medium">{a.name}</div>
        <div className="font-mono text-caption text-faint">{a.key}</div>
      </>
    ),
  },
  {
    id: "template",
    header: t("day2.template"),
    sortValue: (a) => a.template,
    cell: (a) => (
      <>
        <div className="font-mono text-secondary">{a.template}</div>
        <div className="text-caption text-faint">
          {a.workflow ? t("day2.workflowId", { id: a.template_id }) : t("day2.jobTemplateId", { id: a.template_id })}
          {a.fields && a.fields.length > 0 ? t("day2.asksFor", { fields: a.fields.join(", ") }) : t("day2.noSurvey")}
        </div>
      </>
    ),
  },
  {
    id: "offered",
    header: t("day2.offeredOn"),
    cell: (a) => (
      <div className="text-secondary">
        <div>{a.types.map((x) => targetLabel(t, x)).join(", ")}</div>
        <div className="mt-0.5 font-mono text-caption text-muted">{a.condition || t("day2.everywhere")}</div>
      </div>
    ),
  },
  {
    id: "status",
    header: t("day2.status"),
    cell: (a) => (
      <>
        <Status a={a} />
        {a.warnings.length > 0 && (
          <ul
            aria-label={t("day2.warningsFor", { name: a.name })}
            className="mt-1 flex flex-col gap-0.5 text-secondary text-muted"
          >
            {a.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </>
    ),
  },
  {
    id: "form",
    header: t("day2.form"),
    cell: (a) => <FormState f={forms.get(a.key)} base={t("day2.survey")} />,
  },
  {
    id: "synced",
    header: t("day2.synced"),
    align: "end",
    sortValue: (a) => a.synced_at,
    cell: (a) => (
      <span className="text-secondary text-muted" title={fullDate(a.synced_at)}>
        {relative(a.synced_at)}
      </span>
    ),
  },
];

const workflowKey = (w: WorkflowRegistration) => `wf.${w.key}`;

const workflowColumnsOf = (t: TFunction<"admin">, forms: Forms): Column<WorkflowRegistration>[] => [
  {
    id: "action",
    header: t("day2.action"),
    sortValue: (w) => w.day2?.name ?? w.title,
    cell: (w) => (
      <>
        <div className="font-medium">{w.day2?.name ?? w.title}</div>
        <div className="font-mono text-caption text-faint">{workflowKey(w)}</div>
      </>
    ),
  },
  {
    id: "workflow",
    header: t("day2.workflow"),
    sortValue: (w) => w.title,
    cell: (w) => (
      <>
        <div className="text-secondary">{w.title}</div>
        <div className="font-mono text-caption text-faint">{w.workflow_type}</div>
      </>
    ),
  },
  {
    id: "offered",
    header: t("day2.offeredOn"),
    cell: (w) => (
      <div className="text-secondary">
        {w.day2?.offered && w.enabled ? (
          <>
            <div>{w.day2.types.map((x) => targetLabel(t, x)).join(", ")}</div>
            <div className="mt-0.5 font-mono text-caption text-muted">{w.day2.condition || t("day2.everywhere")}</div>
          </>
        ) : (
          <Badge tone="neutral">{t("day2.off")}</Badge>
        )}
      </div>
    ),
  },
  {
    id: "form",
    header: t("day2.form"),
    cell: (w) => <FormState f={forms.get(workflowKey(w))} base={t("day2.inputSchema")} />,
  },
];

function WorkflowActions({ forms }: { forms: Forms }) {
  const { t } = useTranslation("admin");
  const navigate = useNavigate();
  const workflows = useQuery(adm.workflows());
  const rows = workflows.data?.items.filter((w) => w.usages.includes("day2_action") && w.day2);
  return (
    <Panel
      title={t("day2.fromWorkflows")}
      description={
        <Trans
          t={t}
          i18nKey="day2.fromWorkflowsDescription"
          components={{
            workflows: <Link to="/admin/workflows" className="text-accent underline underline-offset-2" />,
          }}
        />
      }
      flush
    >
      <DataTable
        label={t("day2.workflowActions")}
        columns={workflowColumnsOf(t, forms)}
        rows={rows}
        rowKey={workflowKey}
        minWidth={720}
        loading={workflows.isPending}
        error={workflows.isError && <ErrorState error={workflows.error} onRetry={() => void workflows.refetch()} />}
        empty={
          <EmptyState icon={Workflow} title={t("day2.noWorkflowActions")} body={t("day2.noWorkflowActionsBody")} />
        }
        rowMenuLabel={(w) => t("day2.actionsFor", { name: w.day2?.name ?? w.title })}
        rowMenu={(w) => (
          <MenuItem
            onSelect={() => void navigate({ to: "/admin/day2-actions/$key/form", params: { key: workflowKey(w) } })}
          >
            {t("day2.design")}
          </MenuItem>
        )}
      />
    </Panel>
  );
}

export function Day2ActionsPage() {
  const { t } = useTranslation(["admin", "shell"]);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const actions = useQuery(q.awxActions());
  const forms = useForms();
  const patch = usePatch();
  const [editing, setEditing] = useState<AwxAction | null>(null);
  const sync = useMutation({
    mutationFn: () => call(api.POST("/admin/awx/templates/sync")),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.awxActions });
      void qc.invalidateQueries({ queryKey: keys.awxTemplates });
      toast.success(t("day2.syncedToast"));
    },
    onError: (e) => failed(e, t("day2.syncFailed")),
  });
  const syncButton = (
    <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
      {sync.isPending ? t("day2.syncing") : t("day2.sync")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.day2Actions")}
        description={
          <Trans
            t={t}
            i18nKey="day2.description"
            components={{
              policies: (
                <Link
                  to="/admin/policies"
                  search={{ tab: "day2" }}
                  className="text-accent underline underline-offset-2"
                />
              ),
            }}
          />
        }
      />
      <Panel
        title={t("day2.fromAwx")}
        description={t("day2.fromAwxDescription")}
        flush
        actions={actions.data?.items.length ? syncButton : undefined}
      >
        <DataTable
          label={t("day2.awxActions")}
          columns={columnsOf(t, forms)}
          rows={actions.data?.items}
          rowKey={(a) => a.key}
          minWidth={1000}
          loading={actions.isPending}
          error={actions.isError && <ErrorState error={actions.error} onRetry={() => void actions.refetch()} />}
          empty={
            <EmptyState
              icon={Wrench}
              title={t("day2.noAwxActions")}
              body={t("day2.noAwxActionsBody")}
              action={syncButton}
            />
          }
          rowMenuLabel={(a) => t("day2.actionsFor", { name: a.name })}
          rowMenu={(a) => (
            <>
              <MenuItem onSelect={() => setEditing(a)}>{t("day2.changeItem")}</MenuItem>
              <MenuItem onSelect={() => void navigate({ to: "/admin/day2-actions/$key/form", params: { key: a.key } })}>
                {t("day2.design")}
              </MenuItem>
              <MenuSeparator />
              <MenuItem
                disabled={patch.isPending || (a.gone && !a.enabled)}
                onSelect={() =>
                  patch.mutate(
                    {
                      a,
                      body: { enabled: !a.enabled },
                      done: a.enabled
                        ? t("day2.noLongerOffered", { name: a.name })
                        : t("day2.isOffered", { name: a.name }),
                    },
                    { onError: (e) => failed(e, t("day2.notSaved")) },
                  )
                }
              >
                {a.enabled ? t("day2.stopOffering") : t("day2.offerOnDeployments")}
              </MenuItem>
            </>
          )}
        />
      </Panel>
      <WorkflowActions forms={forms} />
      {editing && <ActionSheet a={editing} onClose={() => setEditing(null)} />}
    </Page>
  );
}
