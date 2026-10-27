// The form designer (ADR-0019): fields, a live preview as a chosen user and
// project, and the selected field's properties. Every change saves the draft,
// so the server checks it (rules, sources) and the preview shows its results.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { EmptyState, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { FormRenderer } from "@/forms/FormRenderer";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemVersion } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { FieldList, type Selection } from "./FieldList";
import { sortProblems, type Def, type Problems } from "./model";
import { Properties, type SourceTest } from "./Properties";

interface PreviewContext {
  projectId: string;
  /** Empty: the designer themselves. */
  asUserId: string;
  environment: string;
  values: Record<string, unknown>;
}

type SaveState = "saved" | "saving" | "invalid" | "failed";

function Preview({
  item,
  draftId,
  revision,
  ctx,
  setCtx,
  asUser,
  initialValues,
}: {
  item: AdminCatalogItem;
  draftId: string;
  revision: number;
  ctx: PreviewContext;
  setCtx: (f: (c: PreviewContext) => PreviewContext) => void;
  asUser: string;
  initialValues: Record<string, unknown>;
}) {
  const projects = item.entitlements.projects ?? [];
  const environments = useQuery(q.environments());
  const users = useQuery({
    queryKey: [...keys.catalogAdmin.all, "preview-users", ctx.projectId],
    queryFn: ({ signal }) =>
      call(api.GET("/admin/catalog/preview-users", { params: { query: { project_id: ctx.projectId } }, signal })),
    enabled: Boolean(ctx.projectId),
  });
  const inputs = useDebounced(ctx.values, 300);
  const preview = useQuery({
    queryKey: [
      ...keys.catalogAdmin.all,
      "designer-preview",
      item.id,
      draftId,
      revision,
      ctx.projectId,
      asUser,
      ctx.environment,
      inputs,
    ],
    queryFn: ({ signal }) =>
      call(
        api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/preview", {
          params: { path: { itemId: item.id, versionId: draftId } },
          body: { project_id: ctx.projectId, environment: ctx.environment, as_user_id: asUser, inputs },
          signal,
        }),
      ),
    enabled: Boolean(ctx.projectId && asUser),
    placeholderData: keepPreviousData,
  });
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [checkAll, setCheckAll] = useState(false);
  const errors = Object.fromEntries(
    (preview.data?.errors ?? [])
      .map((e) => [e.field.replace(/^\/inputs\//, ""), e.message] as const)
      .filter(([name]) => checkAll || checked.has(name)),
  );
  const problemCount = preview.data?.errors.length ?? 0;
  return (
    <Panel
      title="Preview"
      actions={
        <Button size="sm" variant="ghost" onClick={() => setCtx((c) => ({ ...c, values: { ...initialValues } }))}>
          Reset inputs
        </Button>
      }
    >
      <div className="mb-3 grid grid-cols-1 gap-2 sm:grid-cols-3">
        <Field label="Project" htmlFor="preview-project">
          <Select
            id="preview-project"
            value={ctx.projectId}
            onChange={(v) => setCtx((c) => ({ ...c, projectId: v, asUserId: "" }))}
            options={projects.map((p) => ({ value: p.id, label: p.name }))}
          />
        </Field>
        <Field label="Preview as" htmlFor="preview-user">
          <Select
            id="preview-user"
            value={ctx.asUserId || "me"}
            loading={users.isPending && Boolean(ctx.projectId)}
            onChange={(v) => setCtx((c) => ({ ...c, asUserId: v === "me" ? "" : v }))}
            options={[
              { value: "me", label: "Myself" },
              ...(users.data?.items ?? []).map((u) => ({
                value: u.id,
                label: `${u.name} (${u.role})`,
                ...(u.email ? { meta: u.email } : {}),
              })),
            ]}
          />
        </Field>
        <Field label="Environment" htmlFor="preview-environment">
          <Select
            id="preview-environment"
            value={ctx.environment}
            onChange={(v) => setCtx((c) => ({ ...c, environment: v }))}
            options={(environments.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
          />
        </Field>
      </div>
      {preview.isPending && <Skeleton className="h-64" />}
      {preview.isError && <ErrorState error={preview.error} onRetry={() => void preview.refetch()} />}
      {preview.data && (
        <>
          <FormRenderer
            form={preview.data.form}
            values={ctx.values}
            onChange={(name, v) => setCtx((c) => ({ ...c, values: { ...c.values, [name]: v } }))}
            onBlur={(name) => setChecked((s) => new Set(s).add(name))}
            errors={errors}
            fieldStates={preview.data.fields}
            ctx={{
              versionId: draftId,
              projectId: ctx.projectId,
              environment: ctx.environment,
              asUserId: asUser,
              revision,
            }}
          />
          <div className="mt-3 flex items-center justify-between gap-2 text-xs text-muted" role="status">
            <span>
              {problemCount === 0
                ? "A request with these inputs would pass."
                : `A request with these inputs would have ${problemCount} problem${problemCount === 1 ? "" : "s"}.`}
            </span>
            {problemCount > 0 && !checkAll && (
              <Button size="sm" variant="ghost" onClick={() => setCheckAll(true)}>
                Show them
              </Button>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}

function Designer({ item, draft, initial }: { item: AdminCatalogItem; draft: CatalogItemVersion; initial: Def }) {
  const qc = useQueryClient();
  const { me, projectId } = useCurrentProject();
  const projects = item.entitlements.projects ?? [];
  const [def, setDef] = useState(initial);
  const [selection, setSelection] = useState<Selection>(() => {
    const first = initial.ui_schema.layout.sections[0]?.fields[0];
    return first ? { kind: "field", name: first } : null;
  });
  const [problems, setProblems] = useState<Problems>(() => sortProblems([]));
  const [state, setState] = useState<SaveState>("saved");
  const [revision, setRevision] = useState(0);
  const [ctx, setCtx] = useState<PreviewContext>(() => ({
    projectId: projects.some((p) => p.id === projectId) ? projectId : (projects[0]?.id ?? ""),
    asUserId: "",
    environment: "dev",
    values: { ...initial.initial_values },
  }));
  const asUser = ctx.asUserId || me.user.id;
  const pending = useRef<Def | null>(null);
  const seq = useRef(0);

  const save = (body: Def) => {
    const mine = ++seq.current;
    call(
      api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
        params: { path: { itemId: item.id, versionId: draft.id } },
        body: { definition: body },
      }),
    ).then(
      () => {
        void qc.invalidateQueries({ queryKey: keys.catalogAdmin.version(item.id, draft.id) });
        if (mine !== seq.current) return;
        setProblems(sortProblems([]));
        setState("saved");
        setRevision((r) => r + 1);
      },
      (e: unknown) => {
        if (mine !== seq.current) return;
        if (isApiError(e) && e.status === 422) {
          setProblems(sortProblems(e.fieldErrors));
          setState("invalid");
        } else {
          setState("failed");
          toast.error("The draft wasn't saved", { description: isApiError(e) ? e.problem.title : String(e) });
        }
      },
    );
  };

  useEffect(() => {
    if (!pending.current) return;
    const t = setTimeout(() => {
      const body = pending.current;
      pending.current = null;
      if (body) save(body);
    }, 600);
    return () => clearTimeout(t);
    // Saved after a pause in changes; save itself only uses refs and setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [def]);

  // Leaving the tab saves what is left.
  useEffect(
    () => () => {
      if (pending.current) save(pending.current);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const change = (next: Def) => {
    pending.current = next;
    setDef(next);
    setState("saving");
  };

  const test: SourceTest = {
    saved: state === "saved",
    values: ctx.values,
    run: (field) =>
      call(
        api.POST("/forms/values", {
          body: {
            version_id: draft.id,
            project_id: ctx.projectId,
            environment: ctx.environment,
            field,
            inputs: ctx.values,
            as_user_id: asUser,
          },
        }),
      ),
  };

  const fieldProblems = Object.keys(problems.fields).length + Object.keys(problems.sections).length;
  const total = fieldProblems + problems.other.length;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p role="status" className="text-xs text-muted">
          Draft {draft.number}:{" "}
          {
            {
              saved: "saved",
              saving: "saving…",
              invalid: `not saved, ${total} problem${total === 1 ? "" : "s"} to fix`,
              failed: "not saved, the server didn't answer",
            }[state]
          }
        </p>
      </div>
      {problems.other.length > 0 && (
        <ul aria-label="Problems outside the form" className="flex flex-col gap-0.5 text-xs text-bad">
          {problems.other.map((e, i) => (
            <li key={i}>
              <span className="font-mono">{e.field.replace(/^\/definition/, "")}</span>: {e.message}
            </li>
          ))}
        </ul>
      )}
      <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-[230px_minmax(0,1fr)] xl:grid-cols-[230px_minmax(0,1fr)_320px]">
        <FieldList def={def} selection={selection} onSelect={setSelection} onChange={change} problems={problems} />
        <div className="min-w-0 lg:order-last lg:col-span-2 xl:order-none xl:col-span-1">
          <Preview
            item={item}
            draftId={draft.id}
            revision={revision}
            ctx={ctx}
            setCtx={setCtx}
            asUser={asUser}
            initialValues={def.initial_values}
          />
        </div>
        <Properties
          def={def}
          selection={selection}
          onSelect={setSelection}
          onChange={change}
          problems={problems}
          test={test}
        />
      </div>
    </div>
  );
}

function DraftDesigner({ item, draft }: { item: AdminCatalogItem; draft: CatalogItemVersion }) {
  const version = useQuery({ ...q.catalogVersion(item.id, draft.id), staleTime: Infinity });
  if (version.isPending) return <Skeleton className="h-96" />;
  if (version.isError) return <ErrorState error={version.error} onRetry={() => void version.refetch()} />;
  return <Designer item={item} draft={draft} initial={version.data.definition} />;
}

export function FormDesigner({ item }: { item: AdminCatalogItem }) {
  const qc = useQueryClient();
  const newDraft = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} })),
    onSuccess: (v) => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(`Draft ${v.number} made from the published version`);
    },
    onError: (e) => toast.error(isApiError(e) ? (e.problem.detail ?? e.problem.title) : "No draft was made"),
  });
  if (!item.draft_version) {
    return (
      <Panel title="Form designer">
        <EmptyState
          title="No draft to change"
          body="The designer changes a draft. Make one from the published version; nobody sees it until you publish it."
          action={
            <Button variant="primary" size="sm" disabled={newDraft.isPending} onClick={() => newDraft.mutate()}>
              New draft
            </Button>
          }
        />
      </Panel>
    );
  }
  return <DraftDesigner key={item.draft_version.id} item={item} draft={item.draft_version} />;
}
