// The form designer (ADR-0019): fields, a live preview as a member of a
// chosen project, and the selected field's properties. Changes go to the
// draft's working copy (draftStore), which saves them; the server checks
// each save (rules, sources) and the preview shows its results.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { EmptyState, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { FormRenderer } from "@/forms/FormRenderer";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemVersion, FieldStates } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import {
  closeDraft,
  editDraft,
  flushDraft,
  openDraft,
  resetDraft,
  saveOver,
  useDraftCopy,
  type DraftCopy,
  type Saver,
} from "./draftStore";
import { FieldList, type Selection } from "./FieldList";
import { sortProblems, type Def } from "./model";
import { Properties, type SourceTest } from "./Properties";

interface PreviewContext {
  projectId: string;
  /** Empty: the designer, if a member, else the project's first member. */
  asUserId: string;
  environment: string;
  values: Record<string, unknown>;
}

/** The members of a project to preview as, and who is chosen. */
function usePreviewUsers(projectId: string, chosen: string, me: string) {
  const users = useQuery({
    queryKey: [...keys.catalogAdmin.all, "preview-users", projectId],
    queryFn: ({ signal }) =>
      call(api.GET("/admin/catalog/preview-users", { params: { query: { project_id: projectId } }, signal })),
    enabled: Boolean(projectId),
  });
  const list = users.data?.items ?? [];
  const asUser = chosen || (list.some((u) => u.id === me) ? me : (list[0]?.id ?? ""));
  return { users, list, asUser };
}

function Preview({
  item,
  draftId,
  revision,
  ctx,
  setCtx,
  me,
  initialValues,
  onStates,
}: {
  item: AdminCatalogItem;
  draftId: string;
  revision: number;
  ctx: PreviewContext;
  setCtx: (f: (c: PreviewContext) => PreviewContext) => void;
  me: string;
  initialValues: Record<string, unknown>;
  onStates: (s: FieldStates | undefined) => void;
}) {
  const projects = item.entitlements.projects ?? [];
  const environments = useQuery(q.environments());
  const { users, list, asUser } = usePreviewUsers(ctx.projectId, ctx.asUserId, me);
  const inputs = useDebounced(ctx.values, 300);
  // Keyed on the server's revision of the draft: the same revision is the same form.
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
  const states = preview.data?.fields;
  useEffect(() => onStates(states), [states, onStates]);
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
            value={asUser}
            placeholder="Nobody"
            loading={users.isPending && Boolean(ctx.projectId)}
            onChange={(v) => setCtx((c) => ({ ...c, asUserId: v }))}
            options={list.map((u) => ({
              value: u.id,
              label: `${u.name} (${u.role})${u.id === me ? " · you" : ""}`,
              ...(u.email ? { meta: u.email } : {}),
            }))}
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
      {users.data && list.length === 0 && (
        <p className="text-xs text-muted">This project has no members to preview as: choose another project.</p>
      )}
      {preview.isPending && asUser !== "" && <Skeleton className="h-64" />}
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

/** What the designer says about saving, and the choices a conflict needs. */
function SaveStatus({ item, draft, copy }: { item: AdminCatalogItem; draft: CatalogItemVersion; copy: DraftCopy }) {
  const qc = useQueryClient();
  const total = copy.problems.length;
  const reload = useMutation({
    // overwrite: save the copy over the server's draft, else take the server's.
    mutationFn: (_overwrite: boolean) =>
      call(
        api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", {
          params: { path: { itemId: item.id, versionId: draft.id } },
        }),
      ),
    onSuccess: (v, overwrite) => {
      if (v.state !== "draft") {
        closeDraft(draft.id);
        void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
        return;
      }
      qc.setQueryData(keys.catalogAdmin.version(item.id, draft.id), v);
      if (overwrite) saveOver(draft.id, v.revision);
      else resetDraft(draft.id, v.definition, v.revision);
    },
    onError: () => {
      // Discarded elsewhere.
      closeDraft(draft.id);
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
    },
  });
  if (copy.state === "conflict") {
    return (
      <div
        role="alert"
        className="flex flex-wrap items-center gap-2 rounded-md border border-warn bg-warn-soft px-3 py-2 text-xs"
      >
        <span className="min-w-0 flex-1">
          Draft {draft.number} changed elsewhere since you opened it ({copy.error ?? "saved, published or discarded"}).
          Your last changes are not saved.
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
  return (
    <p role="status" className="text-xs text-muted">
      Draft {draft.number}:{" "}
      {
        {
          saved: "saved",
          saving: "saving…",
          invalid: `not saved, ${total} problem${total === 1 ? "" : "s"} to fix`,
          failed: `not saved (${copy.error ?? "the server didn't answer"}); the next change tries again`,
        }[copy.state]
      }
    </p>
  );
}

function Designer({ item, draft, copy }: { item: AdminCatalogItem; draft: CatalogItemVersion; copy: DraftCopy }) {
  const qc = useQueryClient();
  const { me, projectId } = useCurrentProject();
  const projects = item.entitlements.projects ?? [];
  const def = copy.def;
  const problems = sortProblems(copy.problems);
  const [selection, setSelection] = useState<Selection>(() => {
    const first = def.ui_schema.layout.sections[0]?.fields[0];
    return first ? { kind: "field", name: first } : null;
  });
  const [states, setStates] = useState<FieldStates | undefined>();
  const [ctx, setCtx] = useState<PreviewContext>(() => ({
    projectId: projects.some((p) => p.id === projectId) ? projectId : (projects[0]?.id ?? ""),
    asUserId: "",
    environment: "dev",
    values: { ...def.initial_values },
  }));
  const { asUser } = usePreviewUsers(ctx.projectId, ctx.asUserId, me.user.id);

  // Leaving the designer saves what is pending.
  useEffect(() => () => flushDraft(draft.id), [draft.id]);

  const save: Saver = async (body, base) => {
    const v = await call(
      api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
        params: { path: { itemId: item.id, versionId: draft.id } },
        body: { definition: body, base_revision: base },
      }),
    );
    // The YAML view and the next visit read this, not an older copy.
    qc.setQueryData(keys.catalogAdmin.version(item.id, draft.id), v);
    return v.revision;
  };
  const change = (next: Def) => editDraft(draft.id, next, save);

  const test: SourceTest = {
    saved: copy.state === "saved",
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

  return (
    <div className="flex flex-col gap-3">
      <SaveStatus item={item} draft={draft} copy={copy} />
      {problems.other.length > 0 && (
        <div className="text-xs text-bad">
          <ul aria-label="Problems outside the form" className="flex flex-col gap-0.5">
            {problems.other.map((e, i) => (
              <li key={i}>
                <span className="font-mono">{e.field.replace(/^\/definition/, "")}</span>: {e.message}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-muted">The Definition tab shows these on their lines, with your unsaved changes.</p>
        </div>
      )}
      <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-[230px_minmax(0,1fr)] xl:grid-cols-[230px_minmax(0,1fr)_320px]">
        <FieldList def={def} selection={selection} onSelect={setSelection} onChange={change} problems={problems} />
        <div className="min-w-0 lg:order-last lg:col-span-2 xl:order-none xl:col-span-1">
          <Preview
            item={item}
            draftId={draft.id}
            revision={copy.revision}
            ctx={ctx}
            setCtx={setCtx}
            me={me.user.id}
            initialValues={def.initial_values}
            onStates={setStates}
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
        />
      </div>
    </div>
  );
}

function DraftDesigner({ item, draft }: { item: AdminCatalogItem; draft: CatalogItemVersion }) {
  const copy = useDraftCopy(draft.id);
  // Fetched again on every visit: a copy is opened from what the server has now.
  const version = useQuery({ ...q.catalogVersion(item.id, draft.id), refetchOnMount: "always" });
  const fresh = version.isFetchedAfterMount && version.data;
  useEffect(() => {
    if (fresh) openDraft(draft.id, fresh.definition, fresh.revision);
  }, [fresh, draft.id]);
  if (version.isError && !copy) return <ErrorState error={version.error} onRetry={() => void version.refetch()} />;
  if (!copy || (!fresh && !copy.dirty)) return <Skeleton className="h-96" />;
  return <Designer item={item} draft={draft} copy={copy} />;
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
