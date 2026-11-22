// The form an administrator designs for a custom day-2 action (ADR-0050):
// the catalog designer's field list, properties and preview, the preview
// worked out on a deployment or VM they pick, as them. Changes save as they
// edit; the action asks for the form once it is turned on.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Ellipsis } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button, IconButton } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Field } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { FormRenderer } from "@/forms/FormRenderer";
import { sentInputs } from "@/forms/defaults";
import { mapFieldErrors } from "@/forms/errors";
import { api, call, isApiError } from "@/lib/api/client";
import type { Day2FormDefinition, Day2FormDesign, FieldError, FieldStates, VmResource } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { keys, q } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import {
  closeDraft,
  draftCopy,
  editDraft,
  flushDraft,
  openDraft,
  resetDraft,
  saveOver,
  useDraftCopy,
  type DraftCopy,
  type Saver,
} from "./catalog/designer/draftStore";
import { FieldList, type Selection } from "./catalog/designer/FieldList";
import { sortProblems, type Def } from "./catalog/designer/model";
import { Properties, type SourceTest } from "./catalog/designer/Properties";
import { failed } from "./governance/common";

const draftIdOf = (action: string) => `day2form:${action}`;
const toDef = (d: Day2FormDefinition): Def => ({ ...d, blueprint: {} });
const toDefinition = ({ form_schema, ui_schema, initial_values }: Def): Day2FormDefinition => ({
  form_schema,
  ui_schema,
  initial_values,
});
/** What the action asks for while its designed form is off. */
const baseForm = (d: Day2FormDesign) => (d.kind === "workflow" ? "the input schema's form" : "the AWX survey");
// The target select's value for the whole deployment: a Radix select has no empty value.
const whole = "deployment";

function useDesign(action: string) {
  return useQuery({
    queryKey: keys.day2Form(action),
    queryFn: ({ signal }) => call(api.GET("/admin/day2-forms/{action}", { params: { path: { action } }, signal })),
    // A copy is opened from what the server has now.
    refetchOnMount: "always",
  });
}

interface PreviewContext {
  /** Empty: the first deployment listed. */
  deploymentId: string;
  /** A VM's ID or `whole`; empty: the first that fits the action. */
  resource: string;
  values: Record<string, unknown>;
}

/** The deployment and VM the preview runs on, from those the administrator sees. */
function usePreviewTarget(design: Day2FormDesign, ctx: PreviewContext) {
  const onDeployment = design.types.includes("deployment");
  const deployments = useQuery({
    queryKey: [...keys.day2Forms, "targets"],
    queryFn: ({ signal }) =>
      call(api.GET("/deployments", { params: { query: { status: ["ready"], limit: 50 } }, signal })),
  });
  const list = deployments.data?.items ?? [];
  const deploymentId = ctx.deploymentId || list[0]?.id || "";
  const deployment = list.find((d) => d.id === deploymentId);
  const detail = useQuery({ ...q.deployment(deploymentId), enabled: Boolean(deploymentId) });
  const vms = (detail.data?.resources ?? []).filter((r): r is VmResource => r.type === "vsphere.vm");
  const resource = ctx.resource || (onDeployment ? whole : (vms[0]?.id ?? ""));
  const resourceId = resource === whole ? undefined : resource || undefined;
  return {
    deployments,
    list,
    deploymentId,
    deployment,
    vms,
    vmsLoading: detail.isPending && Boolean(deploymentId),
    resource,
    resourceId,
    ready: Boolean(deployment) && (resourceId !== undefined || (resource === whole && onDeployment)),
  };
}

type Target = ReturnType<typeof usePreviewTarget>;

function Preview({
  design,
  revision,
  target,
  ctx,
  setCtx,
  onStates,
  showing,
  def,
}: {
  design: Day2FormDesign;
  revision: number;
  target: Target;
  ctx: PreviewContext;
  setCtx: (f: (c: PreviewContext) => PreviewContext) => void;
  onStates: (s: FieldStates | undefined) => void;
  showing?: string;
  def: Def;
}) {
  const onDeployment = design.types.includes("deployment");
  const onVM = design.types.includes("vsphere.vm");
  const inputs = useDebounced(ctx.values, 300);
  const sent = sentInputs(def.ui_schema.fields, inputs, def.initial_values);
  // Keyed on the server's revision of the form: the same revision is the same form.
  const preview = useQuery({
    queryKey: [...keys.day2Form(design.action), "preview", revision, target.deploymentId, target.resourceId, sent],
    queryFn: ({ signal }) =>
      call(
        api.POST("/admin/day2-forms/{action}/preview", {
          params: { path: { action: design.action } },
          body: {
            deployment_id: target.deploymentId,
            ...(target.resourceId ? { resource_id: target.resourceId } : {}),
            inputs: sent,
          },
          signal,
        }),
      ),
    enabled: target.ready,
    placeholderData: keepPreviousData,
  });
  const shown = preview.data;
  const states = shown?.fields;
  useEffect(() => onStates(states), [states, onStates]);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [checkAll, setCheckAll] = useState(false);
  const errors = Object.fromEntries(
    Object.entries(mapFieldErrors(shown?.errors).fields).filter(([name]) => checkAll || checked.has(name)),
  );
  const problemCount = shown?.errors.length ?? 0;
  const d = target.deployment;
  return (
    <Panel
      title="Preview"
      actions={
        <Button size="sm" variant="quiet" onClick={() => setCtx((c) => ({ ...c, values: { ...def.initial_values } }))}>
          Reset inputs
        </Button>
      }
    >
      <div className="mb-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Field label="Deployment" htmlFor="preview-deployment">
          <Select
            id="preview-deployment"
            value={target.deploymentId}
            placeholder="None"
            loading={target.deployments.isPending}
            onChange={(v) => setCtx((c) => ({ ...c, deploymentId: v, resource: "" }))}
            options={target.list.map((x) => ({ value: x.id, label: x.name, meta: x.project.name }))}
          />
        </Field>
        {onVM && (
          <Field label="Runs on" htmlFor="preview-resource">
            <Select
              id="preview-resource"
              value={target.resource}
              placeholder="No VM"
              loading={target.vmsLoading}
              onChange={(v) => setCtx((c) => ({ ...c, resource: v }))}
              options={[
                ...(onDeployment ? [{ value: whole, label: "The whole deployment" }] : []),
                ...target.vms.map((v) => ({ value: v.id, label: v.name })),
              ]}
            />
          </Field>
        )}
      </div>
      {target.deployments.data && target.list.length === 0 && (
        <p className="text-secondary text-muted">No deployment is ready to preview on.</p>
      )}
      {d && !target.vmsLoading && !target.ready && (
        <p className="text-secondary text-muted">{d.name} has no VM to preview on: choose another deployment.</p>
      )}
      {preview.isPending && target.ready && <Skeleton className="h-48" />}
      {preview.isError && <ErrorState error={preview.error} onRetry={() => void preview.refetch()} />}
      {shown && d && (
        <>
          <FormRenderer
            form={{ item_id: "", version_id: draftIdOf(design.action), ...shown.form }}
            values={ctx.values}
            onChange={(name, v) => setCtx((c) => ({ ...c, values: { ...c.values, [name]: v } }))}
            onBlur={(name) => setChecked((s) => new Set(s).add(name))}
            errors={errors}
            fieldStates={shown.fields}
            showing={showing}
            ctx={{
              versionId: `day2:${design.action}`,
              projectId: d.project.id,
              environment: d.environment,
              revision,
              day2: {
                deploymentId: target.deploymentId,
                action: design.action,
                resourceId: target.resourceId,
                designer: true,
              },
            }}
          />
          <div className="mt-3 flex items-center justify-between gap-2 text-secondary text-muted" role="status">
            <span>
              {problemCount === 0
                ? "A run with these inputs would pass."
                : `A run with these inputs would have ${problemCount} problem${problemCount === 1 ? "" : "s"}.`}
            </span>
            {problemCount > 0 && !checkAll && (
              <Button size="sm" variant="quiet" onClick={() => setCheckAll(true)}>
                Show them
              </Button>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}

/** What the page says about saving, and the choices a conflict needs. */
function SaveStatus({ design, copy }: { design: Day2FormDesign; copy: DraftCopy }) {
  const qc = useQueryClient();
  const id = draftIdOf(design.action);
  const reload = useMutation({
    // overwrite: save the copy over the server's form, else take the server's.
    mutationFn: (_overwrite: boolean) =>
      call(api.GET("/admin/day2-forms/{action}", { params: { path: { action: design.action } } })),
    onSuccess: (v, overwrite) => {
      qc.setQueryData(keys.day2Form(design.action), v);
      if (overwrite) saveOver(id, v.revision);
      else resetDraft(id, toDef(v.definition), v.revision);
    },
    onError: (e) => failed(e, "The form could not be read again"),
  });
  if (copy.state === "conflict") {
    return (
      <div
        role="alert"
        className="flex flex-wrap items-center gap-2 rounded-md border border-warn bg-warn-soft px-3 py-2 text-secondary"
      >
        <span className="min-w-0 flex-1">
          Someone saved or dropped this form since you opened it. Your last changes are not saved.
        </span>
        <Button size="sm" onClick={() => reload.mutate(false)} disabled={reload.isPending}>
          Load theirs
        </Button>
        <Button size="sm" variant="danger" onClick={() => reload.mutate(true)} disabled={reload.isPending}>
          Save mine over theirs
        </Button>
      </div>
    );
  }
  const total = copy.problems.length;
  const text =
    copy.revision === 0 && !copy.dirty
      ? `Not designed yet: this is ${baseForm(design)}, to start from. Your first change saves a draft.`
      : {
          saved: design.active
            ? "Saved: people get this form from their next run."
            : `Saved as a draft: people get ${baseForm(design)} until you turn this form on.`,
          saving: "Saving…",
          invalid: `Not saved, ${total} problem${total === 1 ? "" : "s"} to fix`,
          failed: `Not saved (${copy.error ?? "the server didn't answer"}); the next change tries again`,
        }[copy.state];
  return (
    <p role="status" className="text-secondary text-muted">
      {text}
    </p>
  );
}

function Designer({
  design,
  copy,
  refusal,
}: {
  design: Day2FormDesign;
  copy: DraftCopy;
  /** Why turning the form on was refused, until the next change. */
  refusal: FieldError[];
}) {
  const qc = useQueryClient();
  const id = draftIdOf(design.action);
  const def = copy.def;
  const problems = sortProblems([...copy.problems, ...refusal]);
  const [selection, setSelection] = useState<Selection>(() => {
    const first = def.ui_schema.layout.sections[0]?.fields[0];
    return first ? { kind: "field", name: first } : null;
  });
  const [states, setStates] = useState<FieldStates | undefined>();
  const onStates = useCallback((s: FieldStates | undefined) => setStates(s), []);
  const [ctx, setCtx] = useState<PreviewContext>(() => ({
    deploymentId: "",
    resource: "",
    values: { ...def.initial_values },
  }));
  const target = usePreviewTarget(design, ctx);

  // Leaving the designer saves what is pending.
  useEffect(() => () => flushDraft(id), [id]);

  const save: Saver = async (body, base) => {
    const v = await call(
      api.PUT("/admin/day2-forms/{action}", {
        params: { path: { action: design.action } },
        body: { definition: toDefinition(body), base_revision: base },
      }),
    );
    qc.setQueryData(keys.day2Form(design.action), v);
    void qc.invalidateQueries({ queryKey: keys.day2Forms, exact: true });
    return v.revision;
  };
  const change = (next: Def) => editDraft(id, next, save);

  const test: SourceTest = {
    saved: copy.state === "saved",
    values: ctx.values,
    run: (field) =>
      call(
        api.POST("/admin/day2-forms/{action}/values", {
          params: { path: { action: design.action } },
          body: {
            deployment_id: target.deploymentId,
            ...(target.resourceId ? { resource_id: target.resourceId } : {}),
            field,
            inputs: ctx.values,
          },
        }),
      ),
  };

  return (
    <div className="flex flex-col gap-3">
      <SaveStatus design={design} copy={copy} />
      {problems.other.length > 0 && (
        <ul aria-label="Problems outside the fields" className="flex flex-col gap-0.5 text-secondary text-bad">
          {problems.other.map((e, i) => (
            <li key={i}>
              <span className="font-mono">{e.field.replace(/^\/definition/, "")}</span>: {e.message}
            </li>
          ))}
        </ul>
      )}
      <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-[230px_minmax(0,1fr)] xl:grid-cols-[230px_minmax(0,1fr)_320px]">
        <FieldList
          def={def}
          selection={selection}
          onSelect={setSelection}
          onChange={change}
          problems={problems}
          scope="action"
        />
        <div className="min-w-0 lg:order-last lg:col-span-2 xl:order-none xl:col-span-1">
          <Preview
            design={design}
            revision={copy.revision}
            target={target}
            ctx={ctx}
            setCtx={setCtx}
            onStates={onStates}
            def={def}
            showing={
              selection?.kind === "section"
                ? selection.key
                : def.ui_schema.layout.sections.find(
                    (s) => selection && selection.kind === "field" && s.fields.includes(selection.name),
                  )?.key
            }
          />
        </div>
        <Properties
          def={def}
          selection={selection}
          onSelect={setSelection}
          onChange={change}
          problems={problems}
          states={states}
          test={test}
          scope="action"
        />
      </div>
    </div>
  );
}

function Warnings({ design }: { design: Day2FormDesign }) {
  if (design.warnings.length === 0) return null;
  const of = design.kind === "workflow" ? "input schema" : "survey";
  return (
    <section
      aria-label={`Where the form and the ${of} differ`}
      className="rounded-md border border-warn bg-warn-soft px-3 py-2 text-secondary"
    >
      <p className="font-medium text-warn">Where the form and the {of} differ</p>
      <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-5">
        {design.warnings.map((w) => (
          <li key={w}>{w}</li>
        ))}
      </ul>
    </section>
  );
}

function Header({
  design,
  copy,
  onRefused,
}: {
  design: Day2FormDesign;
  copy?: DraftCopy;
  onRefused: (errors: FieldError[]) => void;
}) {
  const qc = useQueryClient();
  const id = draftIdOf(design.action);
  const [dropping, setDropping] = useState(false);
  const turn = useMutation({
    mutationFn: ({ active, c }: { active: boolean; c: DraftCopy }) =>
      call(
        api.PUT("/admin/day2-forms/{action}", {
          params: { path: { action: design.action } },
          body: { definition: toDefinition(c.def), base_revision: c.revision, active },
        }),
      ),
    onSuccess: (v) => {
      qc.setQueryData(keys.day2Form(design.action), v);
      onRefused([]);
      void qc.invalidateQueries({ queryKey: keys.day2Forms, exact: true });
      toast.success(
        v.active ? `${v.name} asks for this form from its next run` : `${v.name} asks for ${baseForm(v)} again`,
      );
    },
    onError: (e) => {
      if (isApiError(e) && e.status === 422) {
        onRefused(e.fieldErrors);
        toast.error("Not turned on", { description: "Fix the problems the designer marks first." });
      } else failed(e, "Not saved");
    },
  });
  const drop = useMutation({
    mutationFn: () => call(api.DELETE("/admin/day2-forms/{action}", { params: { path: { action: design.action } } })),
    onSuccess: () => {
      setDropping(false);
      closeDraft(id);
      onRefused([]);
      void qc.invalidateQueries({ queryKey: keys.day2Forms });
      toast.success(`${design.name} asks for ${baseForm(design)} again`);
    },
    onError: (e) => failed(e, "Not dropped"),
  });
  const pending = !copy || copy.dirty || copy.state !== "saved";
  return (
    <>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/day2-actions">Day-2 actions</Link>
          </Breadcrumbs>
        }
        title={`${design.name} form`}
        description={
          <>
            Once you turn it on, the action&apos;s dialog asks for this form instead of {baseForm(design)}. Its rules
            and sources also read what the action runs on, as <span className="font-mono">resource</span> and{" "}
            <span className="font-mono">deployment</span>.
          </>
        }
        meta={
          <>
            <Badge tone={design.active ? "ok" : design.revision ? "accent" : "neutral"}>
              {design.active ? "On" : design.revision ? "Draft" : "Not designed"}
            </Badge>
            <span className="font-mono">{design.action}</span>
            <span>{design.kind === "workflow" ? "Custom workflow" : "AWX template"}</span>
            {design.updated_at && (
              <span title={fullDate(design.updated_at)}>
                Saved {relative(design.updated_at)}
                {design.updated_by ? ` by ${design.updated_by.name}` : ""}
              </span>
            )}
          </>
        }
        actions={
          <>
            <Button
              variant={design.active ? "secondary" : "primary"}
              disabled={pending || turn.isPending}
              title={pending ? "Wait for your changes to be saved." : undefined}
              onClick={() => copy && turn.mutate({ active: !design.active, c: copy })}
            >
              {design.active ? "Turn off" : "Turn on"}
            </Button>
            <Menu>
              <MenuTrigger asChild>
                <IconButton label={`More actions for ${design.name} form`} variant="secondary" className="size-8">
                  <Ellipsis />
                </IconButton>
              </MenuTrigger>
              <MenuContent>
                <MenuItem danger disabled={design.revision === 0} onSelect={() => setDropping(true)}>
                  Drop the form
                </MenuItem>
              </MenuContent>
            </Menu>
          </>
        }
      />
      <ConfirmDialog
        open={dropping}
        onOpenChange={(o) => !o && setDropping(false)}
        title={`Drop the ${design.name} form?`}
        description={`${design.name} asks for ${baseForm(design)} again, from its next run.`}
        consequences="The form's fields, rules and sources are deleted."
        confirmLabel="Drop the form"
        pending={drop.isPending}
        onConfirm={() => drop.mutate()}
      />
    </>
  );
}

export function Day2FormPage({ actionKey }: { actionKey: string }) {
  const design = useDesign(actionKey);
  const id = draftIdOf(actionKey);
  const copy = useDraftCopy(id);
  const [refusal, setRefusal] = useState<FieldError[]>([]);
  const fresh = design.isFetchedAfterMount && design.data;
  useEffect(() => {
    if (!fresh) return;
    // Revisions start again once a form is dropped: a copy without changes takes what the server has.
    if (draftCopy(id)?.dirty === false) resetDraft(id, toDef(fresh.definition), fresh.revision);
    else openDraft(id, toDef(fresh.definition), fresh.revision);
  }, [fresh, id]);
  // A change after a refusal is checked again by its own save.
  const def = copy?.def;
  useEffect(() => setRefusal([]), [def]);
  if (design.isError && !copy)
    return (
      <Page>
        <ErrorState error={design.error} onRetry={() => void design.refetch()} />
      </Page>
    );
  if (!design.data || !copy || (!fresh && !copy.dirty))
    return (
      <Page>
        <Skeleton className="h-96" />
      </Page>
    );
  return (
    <Page>
      <Header design={design.data} copy={copy} onRefused={setRefusal} />
      <Warnings design={design.data} />
      <Designer key={id} design={design.data} copy={copy} refusal={refusal} />
    </Page>
  );
}
