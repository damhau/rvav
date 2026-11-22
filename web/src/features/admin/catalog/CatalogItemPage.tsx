// A catalog item for catalog admins (ADR-0019): its versions (publish,
// roll back, withdraw, discard, compare, export), its draft's definition as
// YAML, its details and who may request it in which projects.
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { icons, ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemVersion, CatalogItemVersionDetail, FieldError } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { adm, keys, q } from "@/lib/query";
import { cn } from "@/lib/utils";
import { relative } from "@/lib/format";
import { GitBadge, ItemGitTab } from "./GitPanels";
import { SharingTab } from "./SharingTab";
import { ItemChangeDialog, requestableIn } from "./ItemsPanel";
import { fromYaml, problemsOnLines, toYaml } from "./definitionYaml";
import { closeDraft, resetDraft, useDraftCopy } from "./designer/draftStore";
import { FormDesigner } from "./designer/FormDesigner";
import type { EditorHistory, LineProblem, YamlEditorHandle } from "./YamlEditor";

const YamlEditor = lazy(() => import("./YamlEditor"));
// The canvas (React Flow) loads with the Definition tab, not with the page.
const DefinitionGraph = lazy(() => import("./DefinitionGraph"));
const CompositeDesigner = lazy(() => import("./composite/CompositeDesigner"));

/** The editor's text doesn't parse: what is wrong, and where. */
class YamlProblems extends Error {
  constructor(readonly problems: LineProblem[]) {
    super("The definition doesn't parse");
  }
}

const stateTone = { draft: "accent", published: "ok", deprecated: "neutral" } as const;
const stateLabel = { draft: "Draft", published: "Published", deprecated: "Deprecated" } as const;

export const itemTabs = ["versions", "designer", "definition", "details", "entitlements", "git"] as const;
export type ItemTab = (typeof itemTabs)[number];

function useRefresh(itemId: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
    void qc.invalidateQueries({ queryKey: keys.catalog.all });
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.item(itemId) });
    // Their pages say which items read them, and a variable read can't be removed.
    void qc.invalidateQueries({ queryKey: adm.variables().queryKey });
    void qc.invalidateQueries({ queryKey: adm.resourceTypes().queryKey });
  };
}

function toastError(e: unknown, fallback: string) {
  const errors = isApiError(e) ? e.fieldErrors : [];
  toast.error(isApiError(e) ? e.problem.title : fallback, {
    description:
      errors.length > 1 ? (
        <ul className="list-disc pl-4">
          {errors.map((x, i) => (
            <li key={i}>{x.message}</li>
          ))}
        </ul>
      ) : isApiError(e) ? (
        (errors[0]?.message ?? e.problem.detail)
      ) : undefined,
  });
}

/** A publish refused for problems in the draft's definition, shown on their lines and steps. */
interface Refusal {
  versionId: string;
  errors: FieldError[];
}

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/yaml" }));
  const a = Object.assign(document.createElement("a"), { href: url, download: name });
  a.click();
  URL.revokeObjectURL(url);
}

function CompareDialog({ item, onClose }: { item: AdminCatalogItem; onClose: () => void }) {
  const versions = item.versions;
  const [from, setFrom] = useState(versions[1]?.id ?? versions[0]!.id);
  const [to, setTo] = useState(versions[0]!.id);
  const diff = useQuery({ ...q.catalogCompare(item.id, from, to), enabled: from !== to });
  const options = versions.map((v) => ({ value: v.id, label: `${v.number} · ${v.state}` }));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="Compare versions" className="w-[min(760px,calc(100vw-32px))]">
        <div className="flex flex-col gap-3 p-4">
          <div className="flex flex-wrap items-end gap-3">
            <Field label="From" htmlFor="compare-from">
              <Select id="compare-from" value={from} onChange={setFrom} options={options} className="w-48" />
            </Field>
            <Field label="To" htmlFor="compare-to">
              <Select id="compare-to" value={to} onChange={setTo} options={options} className="w-48" />
            </Field>
          </div>
          {from === to && <p className="text-secondary text-muted">Choose two different versions.</p>}
          {diff.isPending && from !== to && <Skeleton className="h-24" />}
          {diff.data && diff.data.changes.length === 0 && <p className="text-secondary text-muted">No difference.</p>}
          {diff.data && diff.data.changes.length > 0 && (
            <ul aria-label="Changes" className="flex max-h-[50vh] flex-col overflow-auto rounded-md border border-line">
              {diff.data.changes.map((c, i) => (
                <li
                  key={i}
                  className="flex items-baseline gap-2 border-b border-line px-3 py-1.5 text-secondary last:border-b-0"
                >
                  <Badge tone={c.change === "added" ? "ok" : c.change === "removed" ? "bad" : "warn"}>{c.change}</Badge>
                  <span className="text-faint">{c.area}</span>
                  <span className="min-w-0 font-mono break-all">{c.summary}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** Why publishing the draft waits: the designer's copy isn't saved, or YAML is typed and not saved. */
function usePublishWait(item: AdminCatalogItem | undefined, yamlDirty: boolean): string | undefined {
  const copy = useDraftCopy(item?.draft_version?.id);
  if (copy?.dirty)
    return copy.state === "saving"
      ? "The designer is still saving the draft."
      : "The designer has changes the server refused or couldn't save: see the Form designer tab.";
  if (yamlDirty) return "Save or undo your changes to the YAML first.";
  return undefined;
}

// keep publishes beside the versions requestable now; retire takes one of several out (ADR-0046).
type VersionAction = "publish" | "keep" | "retire" | "discard" | "export";

/** The item's requestable versions, newest first: the default and those kept published with it. */
function requestable(item: AdminCatalogItem): CatalogItemVersion[] {
  return item.versions.filter((v) => v.state === "published");
}

const versionActionKey = (itemId: string) => ["catalog-version-action", itemId];

/** A version's change is running, from the header or the Versions tab: another waits for it. */
function useVersionBusy(item: AdminCatalogItem): boolean {
  return useIsMutating({ mutationKey: versionActionKey(item.id) }) > 0;
}

function useVersionAction(item: AdminCatalogItem, onRefused?: (r: Refusal) => void) {
  const refresh = useRefresh(item.id);
  return useMutation({
    mutationKey: versionActionKey(item.id),
    mutationFn: async ({ v, action }: { v: CatalogItemVersion; action: VersionAction }) => {
      const path = { itemId: item.id, versionId: v.id };
      switch (action) {
        case "publish":
        case "keep":
          return call(
            api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
              params: { path },
              body: action === "keep" ? { keep_published: true } : {},
            }),
          );
        case "retire":
          return call(
            api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", { params: { path }, body: {} }),
          );
        case "discard":
          return call(api.DELETE("/admin/catalog/items/{itemId}/versions/{versionId}", { params: { path } }));
        case "export": {
          const text = await call(
            api.GET("/admin/catalog/items/{itemId}/export", {
              params: { path: { itemId: item.id }, query: { version_id: v.id } },
              parseAs: "text",
            }),
          );
          download(`${item.slug}-${v.number}.yaml`, String(text));
          return null;
        }
      }
    },
    onSuccess: (_, { v, action }) => {
      if (action === "export") return;
      if (v.state === "draft") closeDraft(v.id); // published or discarded: no draft to edit any more
      refresh();
      const done = {
        publish: v.state === "deprecated" ? `Rolled back to ${v.number}` : `${v.number} published`,
        keep: v.state === "deprecated" ? `${v.number} offered again` : `${v.number} published beside the others`,
        retire: `${v.number} retired`,
        discard: `Draft ${v.number} discarded`,
      }[action];
      toast.success(done);
    },
    onError: (e, { v, action }) => {
      const errors = isApiError(e) && e.status === 422 ? e.fieldErrors : [];
      const publishing = action === "publish" || action === "keep";
      if (!onRefused || !publishing || v.state !== "draft" || !errors.some((x) => x.field.startsWith("/definition")))
        return toastError(e, "That didn't work");
      toast.error(`${v.number} not published`, {
        description: `${errors.length === 1 ? "A problem" : `${errors.length} problems`} in its definition, shown on ${
          item.type === "composite" ? "their lines and steps" : "their lines"
        }.`,
      });
      onRefused({ versionId: v.id, errors });
    },
  });
}

function useNewDraft(item: AdminCatalogItem) {
  const refresh = useRefresh(item.id);
  return useMutation({
    mutationFn: () =>
      call(api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} })),
    onSuccess: (v) => {
      refresh();
      toast.success(`Draft ${v.number} made from the published version`);
    },
    onError: (e) => toastError(e, "No draft was made"),
  });
}

function VersionsTab({
  item,
  wait,
  onWithdraw,
  onRefused,
}: {
  item: AdminCatalogItem;
  /** Why the draft can't be published yet. */
  wait?: string;
  onWithdraw: () => void;
  onRefused: (r: Refusal) => void;
}) {
  const act = useVersionAction(item, onRefused);
  const busy = useVersionBusy(item);
  const [comparing, setComparing] = useState(false);
  const [discarding, setDiscarding] = useState<CatalogItemVersion | null>(null);
  const offered = requestable(item);
  // What publishing beside them keeps: "3.2", or "3.2 and 3.1".
  const kept = (v: CatalogItemVersion) => {
    const others = offered.filter((x) => x.id !== v.id).map((x) => x.number);
    return others.length > 1 ? `${others.slice(0, -1).join(", ")} and ${others.at(-1)}` : (others[0] ?? "");
  };
  const waiting = (label: string) => (busy ? `${label}: waits for the change running` : label);
  const columns: Column<CatalogItemVersion>[] = [
    {
      id: "version",
      header: "Version",
      cell: (v) => (
        <div className="flex items-center gap-2">
          <span className="font-mono font-medium">{v.number}</span>
          <Badge tone={stateTone[v.state]}>{stateLabel[v.state]}</Badge>
          {offered.length > 1 && v.id === item.published_version?.id && <Badge tone="neutral">Default</Badge>}
        </div>
      ),
    },
    {
      id: "changelog",
      header: "Changelog",
      cell: (v) => <span className="text-secondary text-muted">{v.changelog || "None"}</span>,
    },
    {
      id: "made",
      header: "Made",
      cell: (v) => (
        <span className="text-secondary text-muted">
          {v.created_by ? `${v.created_by.name}, ` : ""}
          {relative(v.published_at ?? v.created_at)}
        </span>
      ),
    },
    {
      id: "deployments",
      header: "Deployments",
      align: "end",
      cell: (v) => <span className="tabular-nums">{v.deployments}</span>,
    },
  ];
  return (
    <Panel
      title="Versions"
      description="People request a published version: the newest, unless they pick another kept published. Requests and deployments keep the version they were made with."
      flush
      actions={
        <Button size="sm" disabled={item.versions.length < 2} onClick={() => setComparing(true)}>
          Compare
        </Button>
      }
    >
      <DataTable
        label="Versions"
        columns={columns}
        rows={item.versions}
        rowKey={(v) => v.id}
        minWidth={640}
        rowMenuLabel={(v) => `Actions for version ${v.number}`}
        rowMenu={(v) => (
          <>
            {v.state === "draft" && (
              <MenuItem disabled={Boolean(wait) || busy} onSelect={() => act.mutate({ v, action: "publish" })}>
                {wait ? `Publish ${v.number}: save the draft's changes first` : waiting(`Publish ${v.number}`)}
              </MenuItem>
            )}
            {v.state === "draft" && offered.length > 0 && (
              <MenuItem disabled={Boolean(wait) || busy} onSelect={() => act.mutate({ v, action: "keep" })}>
                {wait
                  ? `Publish ${v.number} beside ${kept(v)}: save the draft's changes first`
                  : waiting(`Publish ${v.number}, keeping ${kept(v)} requestable`)}
              </MenuItem>
            )}
            {v.state === "deprecated" && (
              <MenuItem disabled={busy} onSelect={() => act.mutate({ v, action: "publish" })}>
                {waiting(`Roll back to ${v.number}`)}
              </MenuItem>
            )}
            {v.state === "deprecated" && offered.length > 0 && (
              <MenuItem disabled={busy} onSelect={() => act.mutate({ v, action: "keep" })}>
                {waiting(`Offer ${v.number} again, beside ${kept(v)}`)}
              </MenuItem>
            )}
            {v.state === "published" && offered.length > 1 && (
              <MenuItem disabled={busy} onSelect={() => act.mutate({ v, action: "retire" })}>
                {waiting(`Retire ${v.number}`)}
              </MenuItem>
            )}
            {v.state === "published" && offered.length === 1 && <MenuItem onSelect={onWithdraw}>Withdraw</MenuItem>}
            <MenuItem onSelect={() => act.mutate({ v, action: "export" })}>Export {v.number} as YAML</MenuItem>
            {v.state === "draft" && (
              <>
                <MenuSeparator />
                <MenuItem danger onSelect={() => setDiscarding(v)}>
                  Discard
                </MenuItem>
              </>
            )}
          </>
        )}
      />
      {comparing && <CompareDialog item={item} onClose={() => setComparing(false)} />}
      {discarding && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setDiscarding(null)}
          title={`Discard draft ${discarding.number}?`}
          description="Its changes are lost, in the form designer and the YAML alike."
          consequences={
            item.git?.state === "draft"
              ? "The draft came from the repository: rvav commits its own file there again."
              : item.published_version
                ? `People keep getting ${item.published_version.number}.`
                : "The item has no published version: nobody can request it."
          }
          confirmLabel="Discard draft"
          pending={act.isPending}
          onConfirm={() => act.mutate({ v: discarding, action: "discard" }, { onSuccess: () => setDiscarding(null) })}
        />
      )}
    </Panel>
  );
}

function DefinitionTab({
  item,
  onDirty,
  refusal,
  onRefusalShown,
}: {
  item: AdminCatalogItem;
  onDirty: (dirty: boolean) => void;
  refusal?: Refusal;
  onRefusalShown: () => void;
}) {
  const qc = useQueryClient();
  const shown = item.draft_version ?? item.published_version ?? item.versions[0];
  const version = useQuery({
    ...q.catalogVersion(item.id, shown?.id ?? ""),
    enabled: Boolean(shown),
    refetchOnMount: "always",
  });
  const refresh = useRefresh(item.id);
  // The designer's changes the server hasn't taken (with its problems) are shown, not lost.
  const copy = useDraftCopy(shown?.state === "draft" ? shown.id : undefined);
  const pending = copy?.dirty ? copy : undefined;
  const [text, setText] = useState("");
  const [problems, setProblems] = useState<LineProblem[]>([]);
  // The server's last refusal, with the text it refused: the composite
  // designer shows its problems on their steps while the text is that one.
  const [refused, setRefused] = useState<{ text: string; errors: FieldError[] }>();
  // The composite designer writes into the editor, as steps of its history.
  const editor = useRef<YamlEditorHandle>(null);
  const [editorHistory, setEditorHistory] = useState<EditorHistory>({ canUndo: false, canRedo: false });
  const original = useMemo(
    () => (pending ? toYaml(pending.def) : version.data ? toYaml(version.data.definition) : ""),
    [pending, version.data],
  );
  // The editor shows the original anew only while what it shows is the last
  // one given: the form designer's copy, saved or refused while the author
  // types here (its save goes on after the tab is left), or the draft
  // fetched again, doesn't take their typing and its history away.
  const typed = useRef(text);
  useEffect(() => {
    typed.current = text;
  }, [text]);
  const given = useRef<{ id?: string; text: string } | null>(null);
  useEffect(() => {
    const edited = given.current?.id === shown?.id && given.current?.text !== typed.current;
    given.current = { id: shown?.id, text: original };
    if (edited) return;
    setText(original);
    setProblems(pending ? problemsOnLines(original, pending.problems) : []);
    setRefused(pending?.problems.length ? { text: original, errors: pending.problems } : undefined);
  }, [original, pending, shown?.id]);
  // After the effect above, which shows the draft as fetched: a publish
  // refused shows its problems as a save refused does, once.
  const problemList = useRef<HTMLUListElement>(null);
  useEffect(() => {
    if (!refusal || refusal.versionId !== shown?.id || !version.data || pending) return;
    setProblems(problemsOnLines(original, refusal.errors));
    setRefused({ text: original, errors: refusal.errors });
    onRefusalShown();
    requestAnimationFrame(() => problemList.current?.focus());
  }, [refusal, shown?.id, version.data, pending, original, onRefusalShown]);
  const dirty = text !== original || Boolean(pending);
  // What may not work as meant in the version as saved (ADR-0035), on its lines.
  const warnings = useMemo(
    () => (pending || !version.data?.warnings ? [] : problemsOnLines(original, version.data.warnings)),
    [pending, version.data, original],
  );
  useEffect(() => onDirty(text !== original), [text, original, onDirty]);
  useEffect(() => {
    if (text === original) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [text, original]);
  const editable = shown?.state === "draft";
  const save = useMutation({
    mutationFn: () => {
      const parsed = fromYaml(text);
      if (!parsed.def) throw new YamlProblems(parsed.problems);
      return call(
        api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
          params: { path: { itemId: item.id, versionId: shown!.id } },
          body: { definition: parsed.def, base_revision: pending?.revision ?? version.data?.revision },
        }),
      );
    },
    onSuccess: (v) => {
      const key = keys.catalogAdmin.version(item.id, v.id);
      qc.setQueryData(key, v);
      // What the cache now holds (it keeps its own copy when the content is the
      // same): the text shown is what is stored, without YAML comments.
      const stored = qc.getQueryData<CatalogItemVersionDetail>(key) ?? v;
      resetDraft(v.id, stored.definition, v.revision); // the designer opens what was saved
      setText(toYaml(stored.definition));
      setProblems([]);
      setRefused(undefined);
      refresh();
      toast.success(`Draft ${shown!.number} saved`);
    },
    onError: (e) => {
      if (e instanceof YamlProblems) setProblems(e.problems);
      else if (isApiError(e) && e.fieldErrors.length) {
        setProblems(problemsOnLines(text, e.fieldErrors));
        setRefused({ text, errors: e.fieldErrors });
      } else if (isApiError(e) && e.status === 409) {
        toastError(e, "Saving failed");
        // The draft as it now is, in place of the text refused.
        void version.refetch().then((r) => {
          if (pending || !r.data) return;
          setText(toYaml(r.data.definition));
          setProblems([]);
          setRefused(undefined);
        });
      } else toastError(e, "Saving failed");
    },
  });
  if (!shown) return null;
  const drawn = item.type === "vm_blueprint" || item.type === "composite";
  const designed = item.type === "composite" && editable;
  return (
    <Panel
      title={editable ? `Draft ${shown.number}` : `Version ${shown.number} (read only)`}
      actions={
        editable && (
          <div className="flex gap-2">
            <Button size="sm" variant="quiet" disabled={text === original} onClick={() => setText(original)}>
              Undo changes
            </Button>
            <Button size="sm" variant="primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? "Saving…" : "Save draft"}
            </Button>
          </div>
        )
      }
    >
      {!editable && (
        <p className="mb-2 text-secondary text-muted">
          Published and earlier versions never change: make a draft to edit.
        </p>
      )}
      {pending && (
        <p role="note" className="mb-2 text-secondary text-warn">
          This shows the designer's changes, which the server hasn't saved yet; fix them here and save.
        </p>
      )}
      {version.isPending && <Skeleton className="h-64" />}
      {version.isError && <ErrorState error={version.error} onRetry={() => void version.refetch()} />}
      {/* The YAML and, for the kinds the canvas draws, its graph beside it
          (below it on narrower screens), drawn from the text as it is typed. */}
      <div className={cn("grid gap-4", drawn && "xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]")}>
        <div className="min-w-0">
          {version.data && (
            <Suspense fallback={<Skeleton className="h-64" />}>
              <YamlEditor
                key={shown.id}
                ref={editor}
                onHistory={setEditorHistory}
                value={text}
                onChange={setText}
                readOnly={!editable}
                problems={problems}
                label={`Definition of ${item.name} ${shown.number}`}
              />
            </Suspense>
          )}
          {problems.length > 0 && (
            <ul
              ref={problemList}
              tabIndex={-1}
              aria-label="Problems in the definition"
              className="mt-2 flex flex-col gap-0.5 text-secondary text-bad"
            >
              {problems.map((p, i) => (
                <li key={i}>
                  Line {p.line}: {p.message}
                </li>
              ))}
            </ul>
          )}
          {warnings.length > 0 && (
            <ul aria-label="Warnings" className="mt-2 flex flex-col gap-0.5 text-secondary text-warn">
              {warnings.map((p, i) => (
                <li key={i}>
                  Line {p.line}: {p.message}
                </li>
              ))}
            </ul>
          )}
        </div>
        {drawn && version.data && (
          <Suspense fallback={<Skeleton className="h-64" />}>
            {designed ? (
              <CompositeDesigner
                name={item.name}
                entitlements={item.entitlements}
                design={{
                  text,
                  onEdit: (next) => editor.current?.apply(next),
                  history: editorHistory,
                  undo: () => editor.current?.undo(),
                  redo: () => editor.current?.redo(),
                  serverProblems: refused,
                }}
              />
            ) : (
              <DefinitionGraph text={text} name={item.name} composite={item.type === "composite"} />
            )}
          </Suspense>
        )}
      </div>
    </Panel>
  );
}

function DetailsTab({ item }: { item: AdminCatalogItem }) {
  const refresh = useRefresh(item.id);
  const categories = useQuery(q.adminCatalogCategories());
  const [d, setD] = useState({
    name: item.name,
    summary: item.summary,
    description_md: item.description_md,
    category: item.category,
    icon: item.icon,
    tags: item.tags.join(", "),
    featured: item.featured,
  });
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PATCH("/admin/catalog/items/{itemId}", {
          params: { path: { itemId: item.id } },
          body: {
            ...d,
            tags: d.tags
              .split(",")
              .map((t) => t.trim())
              .filter(Boolean),
          },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success(`${d.name} saved`);
    },
    onError: (e) => toastError(e, "Saving failed"),
  });
  const set = <K extends keyof typeof d>(k: K, v: (typeof d)[K]) => setD((x) => ({ ...x, [k]: v }));
  return (
    <Panel
      title="Details"
      description="How the item shows in the catalog. Saved at once, in every version."
      footer={
        <Button type="submit" form="item-details" variant="primary" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save details"}
        </Button>
      }
    >
      <form
        id="item-details"
        className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <Field label="Name" htmlFor="item-name">
          <Input id="item-name" value={d.name} onChange={(e) => set("name", e.target.value)} maxLength={80} />
        </Field>
        <Field label="Category" htmlFor="item-category">
          <Select
            id="item-category"
            value={d.category}
            onChange={(v) => set("category", v)}
            options={(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name }))}
          />
        </Field>
        <Field label="Summary" htmlFor="item-summary" className="md:col-span-2">
          <Input id="item-summary" value={d.summary} onChange={(e) => set("summary", e.target.value)} maxLength={200} />
        </Field>
        <Field label="Description (Markdown)" htmlFor="item-description" className="md:col-span-2">
          <Textarea
            id="item-description"
            value={d.description_md}
            onChange={(e) => set("description_md", e.target.value)}
          />
        </Field>
        <Field
          label="Tags"
          htmlFor="item-tags"
          hint="Separated by commas. They also group the item's VMs in the AWX inventory."
        >
          <Input id="item-tags" value={d.tags} onChange={(e) => set("tags", e.target.value)} />
        </Field>
        <div className="flex items-center gap-2 self-end pb-1 text-body">
          <Checkbox id="item-featured" checked={d.featured} onChange={(v) => set("featured", v)} />
          <label htmlFor="item-featured">Featured in the catalog</label>
        </div>
        <fieldset className="md:col-span-2">
          <legend className="mb-1.5 text-secondary font-medium">Icon</legend>
          <div role="radiogroup" aria-label="Icon" className="flex flex-wrap gap-1.5">
            {Object.keys(icons).map((name, i, names) => (
              <button
                key={name}
                id={`icon-${name}`}
                type="button"
                role="radio"
                aria-checked={d.icon === name}
                aria-label={name}
                // One tab stop; arrow keys choose, as in a radio group.
                tabIndex={d.icon === name || (!(d.icon in icons) && i === 0) ? 0 : -1}
                onKeyDown={(e) => {
                  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
                  if (!step) return;
                  e.preventDefault();
                  const next = names[(i + step + names.length) % names.length]!;
                  set("icon", next);
                  document.getElementById(`icon-${next}`)?.focus();
                }}
                onClick={() => set("icon", name)}
                className={cn("rounded-md p-0.5", d.icon === name ? "ring-2 ring-accent" : "hover:bg-sunken")}
              >
                <ItemIcon name={name} />
              </button>
            ))}
          </div>
        </fieldset>
      </form>
    </Panel>
  );
}

/** What concerns the whole item: its request form, its YAML, withdrawing and deleting it. */
function ItemMenu({ item, onAsk }: { item: AdminCatalogItem; onAsk: (action: "withdraw" | "delete") => void }) {
  const navigate = useNavigate();
  const { projectId } = useCurrentProject();
  const exportAll = useMutation({
    mutationFn: async () => {
      const text = await call(
        api.GET("/admin/catalog/items/{itemId}/export", { params: { path: { itemId: item.id } }, parseAs: "text" }),
      );
      download(`${item.slug}.yaml`, String(text));
    },
    onError: (e) => toastError(e, "The export failed"),
  });
  return (
    <Menu>
      <MenuTrigger asChild>
        <IconButton label={`More actions for ${item.name}`} variant="secondary" className="size-8">
          <Ellipsis />
        </IconButton>
      </MenuTrigger>
      <MenuContent>
        {requestableIn(item, projectId) && (
          <MenuItem onSelect={() => void navigate({ to: "/catalog/$itemId/request", params: { itemId: item.slug } })}>
            Open the request form
          </MenuItem>
        )}
        <MenuItem onSelect={() => exportAll.mutate()}>Export every version as YAML</MenuItem>
        {item.published_version && <MenuItem onSelect={() => onAsk("withdraw")}>Withdraw</MenuItem>}
        {!item.requested && (
          <>
            <MenuSeparator />
            <MenuItem danger onSelect={() => onAsk("delete")}>
              Delete item
            </MenuItem>
          </>
        )}
      </MenuContent>
    </Menu>
  );
}

/** The page's one primary action: publish the draft, or make one. */
function PrimaryAction({
  item,
  wait,
  onRefused,
}: {
  item: AdminCatalogItem;
  wait?: string;
  onRefused: (r: Refusal) => void;
}) {
  const act = useVersionAction(item, onRefused);
  const busy = useVersionBusy(item);
  const newDraft = useNewDraft(item);
  const draft = item.draft_version;
  if (!draft)
    return (
      <Button variant="primary" disabled={newDraft.isPending} onClick={() => newDraft.mutate()}>
        New draft
      </Button>
    );
  return (
    <>
      {wait && (
        <span id="publish-wait" className="max-w-72 text-secondary text-muted">
          {wait}
        </span>
      )}
      <Button
        variant="primary"
        disabled={Boolean(wait) || busy}
        aria-describedby={wait ? "publish-wait" : undefined}
        onClick={() => act.mutate({ v: draft, action: "publish" })}
      >
        {act.isPending ? "Publishing…" : `Publish ${draft.number}`}
      </Button>
    </>
  );
}

export function CatalogItemPage({ itemId, tab = "versions" }: { itemId: string; tab?: ItemTab }) {
  const navigate = useNavigate();
  const item = useQuery(q.adminCatalogItem(itemId));
  const [yamlDirty, setYamlDirty] = useState(false);
  const [asking, setAsking] = useState<"withdraw" | "delete" | null>(null);
  const [leavingFor, setLeavingFor] = useState<ItemTab | null>(null);
  const [refusal, setRefusal] = useState<Refusal>();
  const shownRefusal = useCallback(() => setRefusal(undefined), []);
  const wait = usePublishWait(item.data, yamlDirty);
  if (item.isPending) {
    return (
      <Page>
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64" />
      </Page>
    );
  }
  if (item.isError) {
    return (
      <Page>
        <ErrorState error={item.error} onRetry={() => void item.refetch()} />
      </Page>
    );
  }
  const it = item.data;
  const shown = tab === "git" && !it.git ? "versions" : tab;
  const openTab = (next: ItemTab) => {
    setYamlDirty(false);
    void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId }, search: { tab: next }, replace: true });
  };
  // Publishing waits while the YAML has changes, so the tab opens without asking.
  const refused = (r: Refusal) => {
    setRefusal(r);
    if (shown !== "definition") openTab("definition");
  };
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/catalog-items">Catalog items</Link>
          </Breadcrumbs>
        }
        title={
          <span className="flex items-center gap-2.5">
            <ItemIcon name={it.icon} />
            {it.name}
          </span>
        }
        meta={
          <>
            <span className="font-mono">{it.slug}</span>
            <span>{itemTypeLabel(it.type)}</span>
            {it.published_version ? (
              <Badge tone="ok">
                Published{" "}
                {requestable(it)
                  .map((v) => v.number)
                  .join(", ")}
              </Badge>
            ) : (
              <Badge tone="neutral">Not published</Badge>
            )}
            {it.draft_version && <Badge tone="accent">Draft {it.draft_version.number}</Badge>}
            {it.git && it.git.state !== "synced" && <GitBadge git={it.git} />}
          </>
        }
        actions={
          <>
            <ItemMenu item={it} onAsk={setAsking} />
            <PrimaryAction item={it} wait={wait} onRefused={refused} />
          </>
        }
      />
      <Tabs
        value={shown}
        // With YAML typed, a focused tab doesn't open: Cancel gives the focus back to the tab that asked.
        activationMode={yamlDirty ? "manual" : "automatic"}
        onValueChange={(next) => {
          if (shown === "definition" && yamlDirty) setLeavingFor(next as ItemTab);
          else openTab(next as ItemTab);
        }}
      >
        <TabsList aria-label="Item sections">
          <TabsTrigger value="versions">Versions</TabsTrigger>
          <TabsTrigger value="designer">Form designer</TabsTrigger>
          <TabsTrigger value="definition">Definition</TabsTrigger>
          <TabsTrigger value="details">Details</TabsTrigger>
          <TabsTrigger value="entitlements">Sharing</TabsTrigger>
          {it.git && <TabsTrigger value="git">Git</TabsTrigger>}
        </TabsList>
        <TabsContent value="versions" className="pt-4">
          <VersionsTab item={it} wait={wait} onWithdraw={() => setAsking("withdraw")} onRefused={refused} />
        </TabsContent>
        <TabsContent value="designer" className="pt-4">
          <FormDesigner item={it} />
        </TabsContent>
        <TabsContent value="definition" className="pt-4">
          <DefinitionTab item={it} onDirty={setYamlDirty} refusal={refusal} onRefusalShown={shownRefusal} />
        </TabsContent>
        <TabsContent value="details" className="pt-4">
          <DetailsTab key={it.id + it.name + it.icon} item={it} />
        </TabsContent>
        <TabsContent value="entitlements" className="pt-4">
          <SharingTab item={it} />
        </TabsContent>
        {it.git && (
          <TabsContent value="git" className="pt-4">
            <ItemGitTab item={it} />
          </TabsContent>
        )}
      </Tabs>
      {asking && (
        <ItemChangeDialog
          change={{ it, action: asking }}
          onClose={() => setAsking(null)}
          onDone={() => (asking === "delete" ? navigate({ to: "/admin/catalog-items" }) : undefined)}
        />
      )}
      <ConfirmDialog
        open={leavingFor !== null}
        onOpenChange={(o) => !o && setLeavingFor(null)}
        title="Leave the YAML without saving?"
        description="Your changes to the definition are lost; the draft keeps what was last saved."
        confirmLabel="Leave without saving"
        onConfirm={() => {
          if (leavingFor) openTab(leavingFor);
          setLeavingFor(null);
        }}
      />
    </Page>
  );
}
