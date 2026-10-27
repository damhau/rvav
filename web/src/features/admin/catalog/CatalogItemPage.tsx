// A catalog item for catalog admins (ADR-0019): its versions (publish,
// roll back, withdraw, discard, compare, export), its draft's definition as
// YAML, its details and the projects that may request it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { icons, ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemVersion } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { cn, relative } from "@/lib/utils";
import { fromYaml, problemsOnLines, toYaml } from "./definitionYaml";
import type { LineProblem } from "./YamlEditor";

const YamlEditor = lazy(() => import("./YamlEditor"));

/** The editor's text doesn't parse: what is wrong, and where. */
class YamlProblems extends Error {
  constructor(readonly problems: LineProblem[]) {
    super("The definition doesn't parse");
  }
}

const stateTone = { draft: "accent", published: "ok", deprecated: "neutral" } as const;

function useRefresh(itemId: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
    void qc.invalidateQueries({ queryKey: keys.catalog.all });
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.item(itemId) });
  };
}

function toastError(e: unknown, fallback: string) {
  toast.error(isApiError(e) ? e.problem.title : fallback, {
    description: isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail) : undefined,
  });
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
          {from === to && <p className="text-xs text-muted">Choose two different versions.</p>}
          {diff.isPending && from !== to && <Skeleton className="h-24" />}
          {diff.data && diff.data.changes.length === 0 && <p className="text-xs text-muted">No difference.</p>}
          {diff.data && diff.data.changes.length > 0 && (
            <ul aria-label="Changes" className="flex max-h-[50vh] flex-col overflow-auto rounded-md border border-line">
              {diff.data.changes.map((c, i) => (
                <li
                  key={i}
                  className="flex items-baseline gap-2 border-b border-line px-3 py-1.5 text-xs last:border-b-0"
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

function VersionsTab({ item }: { item: AdminCatalogItem }) {
  const refresh = useRefresh(item.id);
  const [comparing, setComparing] = useState(false);
  const act = useMutation({
    mutationFn: async ({
      v,
      action,
    }: {
      v: CatalogItemVersion;
      action: "publish" | "deprecate" | "discard" | "export";
    }) => {
      const path = { itemId: item.id, versionId: v.id };
      switch (action) {
        case "publish":
          return call(
            api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", { params: { path }, body: {} }),
          );
        case "deprecate":
          return call(api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", { params: { path } }));
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
      refresh();
      const done = {
        publish: v.state === "deprecated" ? `Rolled back to ${v.number}` : `${v.number} published`,
        deprecate: `${item.name} withdrawn`,
        discard: `Draft ${v.number} discarded`,
      }[action];
      toast.success(done);
    },
    onError: (e) => toastError(e, "That didn't work"),
  });
  const newDraft = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} })),
    onSuccess: (v) => {
      refresh();
      toast.success(`Draft ${v.number} made from the published version`);
    },
    onError: (e) => toastError(e, "No draft was made"),
  });
  return (
    <Panel
      title="Versions"
      flush
      actions={
        <div className="flex gap-2">
          <Button size="sm" disabled={item.versions.length < 2} onClick={() => setComparing(true)}>
            Compare
          </Button>
          <Button
            size="sm"
            variant="primary"
            disabled={Boolean(item.draft_version) || newDraft.isPending}
            onClick={() => newDraft.mutate()}
          >
            New draft
          </Button>
        </div>
      }
    >
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="label text-left">
            <th scope="col" className="border-b border-line px-4 py-2 font-medium">
              Version
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Changelog
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Deployments
            </th>
            <th scope="col" className="border-b border-line px-4 py-2">
              <span className="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {item.versions.map((v) => (
            <tr key={v.id} className="hover:bg-sunken">
              <td className="border-b border-line px-4 py-2 align-top">
                <div className="flex items-center gap-2">
                  <span className="font-mono font-medium">{v.number}</span>
                  <Badge tone={stateTone[v.state]}>{v.state}</Badge>
                </div>
                <div className="text-[11px] text-faint">
                  {v.created_by ? `${v.created_by.name}, ` : ""}
                  {relative(v.published_at ?? v.created_at)}
                </div>
              </td>
              <td className="border-b border-line px-3 py-2 align-top text-xs text-muted">{v.changelog || "—"}</td>
              <td className="border-b border-line px-3 py-2 align-top text-xs text-muted">{v.deployments}</td>
              <td className="border-b border-line px-4 py-2 text-right align-top whitespace-nowrap">
                {v.state === "draft" && (
                  <>
                    <Button size="sm" variant="primary" onClick={() => act.mutate({ v, action: "publish" })}>
                      Publish
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => act.mutate({ v, action: "discard" })}>
                      Discard
                    </Button>
                  </>
                )}
                {v.state === "deprecated" && (
                  <Button
                    size="sm"
                    onClick={() => act.mutate({ v, action: "publish" })}
                    aria-label={`Roll back to ${v.number}`}
                  >
                    Roll back to this
                  </Button>
                )}
                {v.state === "published" && (
                  <Button size="sm" variant="danger" onClick={() => act.mutate({ v, action: "deprecate" })}>
                    Withdraw
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => act.mutate({ v, action: "export" })}
                  aria-label={`Export ${v.number} as YAML`}
                >
                  Export
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {comparing && <CompareDialog item={item} onClose={() => setComparing(false)} />}
    </Panel>
  );
}

function DefinitionTab({ item }: { item: AdminCatalogItem }) {
  const shown = item.draft_version ?? item.published_version ?? item.versions[0];
  const version = useQuery({ ...q.catalogVersion(item.id, shown?.id ?? ""), enabled: Boolean(shown) });
  const refresh = useRefresh(item.id);
  const [text, setText] = useState("");
  const [problems, setProblems] = useState<LineProblem[]>([]);
  const original = useMemo(() => (version.data ? toYaml(version.data.definition) : ""), [version.data]);
  useEffect(() => {
    setText(original);
    setProblems([]);
  }, [original]);
  const editable = shown?.state === "draft";
  const save = useMutation({
    mutationFn: () => {
      const parsed = fromYaml(text);
      if (!parsed.def) throw new YamlProblems(parsed.problems);
      return call(
        api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
          params: { path: { itemId: item.id, versionId: shown!.id } },
          body: { definition: parsed.def },
        }),
      );
    },
    onSuccess: () => {
      setProblems([]);
      refresh();
      toast.success(`Draft ${shown!.number} saved`);
    },
    onError: (e) => {
      if (e instanceof YamlProblems) setProblems(e.problems);
      else if (isApiError(e) && e.fieldErrors.length) setProblems(problemsOnLines(text, e.fieldErrors));
      else toastError(e, "Saving failed");
    },
  });
  if (!shown) return null;
  return (
    <Panel
      title={editable ? `Draft ${shown.number}` : `Version ${shown.number} (read only)`}
      actions={
        editable && (
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" disabled={text === original} onClick={() => setText(original)}>
              Undo changes
            </Button>
            <Button
              size="sm"
              variant="primary"
              disabled={text === original || save.isPending}
              onClick={() => save.mutate()}
            >
              {save.isPending ? "Saving…" : "Save draft"}
            </Button>
          </div>
        )
      }
    >
      {!editable && (
        <p className="mb-2 text-xs text-muted">Published and earlier versions never change: make a draft to edit.</p>
      )}
      {version.isPending && <Skeleton className="h-64" />}
      {version.isError && <ErrorState error={version.error} onRetry={() => void version.refetch()} />}
      {version.data && (
        <Suspense fallback={<Skeleton className="h-64" />}>
          <YamlEditor
            key={shown.id}
            value={text}
            onChange={setText}
            readOnly={!editable}
            problems={problems}
            label={`Definition of ${item.name} ${shown.number}`}
          />
        </Suspense>
      )}
      {problems.length > 0 && (
        <ul aria-label="Problems in the definition" className="mt-2 flex flex-col gap-0.5 text-xs text-bad">
          {problems.map((p, i) => (
            <li key={i}>
              Line {p.line}: {p.message}
            </li>
          ))}
        </ul>
      )}
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
    <Panel title="Details">
      <form
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
        <div className="flex items-center gap-2 self-end pb-1 text-[13px]">
          <Checkbox id="item-featured" checked={d.featured} onChange={(v) => set("featured", v)} />
          <label htmlFor="item-featured">Featured in the catalog</label>
        </div>
        <fieldset className="md:col-span-2">
          <legend className="mb-1.5 text-xs font-medium">Icon</legend>
          <div role="radiogroup" aria-label="Icon" className="flex flex-wrap gap-1.5">
            {Object.keys(icons).map((name) => (
              <button
                key={name}
                type="button"
                role="radio"
                aria-checked={d.icon === name}
                aria-label={name}
                onClick={() => set("icon", name)}
                className={cn("rounded-md p-0.5", d.icon === name ? "ring-2 ring-accent" : "hover:bg-sunken")}
              >
                <ItemIcon name={name} />
              </button>
            ))}
          </div>
        </fieldset>
        <div className="md:col-span-2">
          <Button type="submit" variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save details"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function EntitlementsTab({ item }: { item: AdminCatalogItem }) {
  const refresh = useRefresh(item.id);
  const [all, setAll] = useState(item.entitlements.all_projects);
  const [chosen, setChosen] = useState(new Set(item.entitlements.project_ids));
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", {
          params: { path: { itemId: item.id } },
          body: { all_projects: all, project_ids: all ? [] : [...chosen] },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success("Entitlements saved");
    },
    onError: (e) => toastError(e, "Saving failed"),
  });
  const projects = item.entitlements.projects ?? [];
  return (
    <Panel title="Who may request it">
      <div className="flex max-w-xl flex-col gap-3 text-[13px]">
        <div role="radiogroup" aria-label="Open to" className="flex flex-col gap-1.5">
          {[
            { v: true, label: "Every project", hint: "projects made later included" },
            { v: false, label: "Chosen projects", hint: "" },
          ].map((o) => (
            <label key={o.label} className="flex items-center gap-2">
              <input type="radio" name="open-to" checked={all === o.v} onChange={() => setAll(o.v)} />
              {o.label}
              {o.hint && <span className="text-xs text-faint">{o.hint}</span>}
            </label>
          ))}
        </div>
        {!all && (
          <ul aria-label="Projects" className="flex flex-col gap-1 rounded-md border border-line p-2">
            {projects.map((p) => (
              <li key={p.id} className="flex items-center gap-2">
                <Checkbox
                  id={`project-${p.id}`}
                  checked={chosen.has(p.id)}
                  onChange={(on) =>
                    setChosen((s) => {
                      const n = new Set(s);
                      if (on) n.add(p.id);
                      else n.delete(p.id);
                      return n;
                    })
                  }
                />
                <label htmlFor={`project-${p.id}`}>{p.name}</label>
                <span className="font-mono text-[11px] text-faint">{p.code}</span>
              </li>
            ))}
          </ul>
        )}
        <div>
          <Button variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? "Saving…" : "Save entitlements"}
          </Button>
        </div>
      </div>
    </Panel>
  );
}

export function CatalogItemPage({ itemId }: { itemId: string }) {
  const item = useQuery(q.adminCatalogItem(itemId));
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
  return (
    <Page>
      <PageHeader
        crumbs={
          <Link to="/admin/catalog-items" className="hover:text-fg">
            Catalog items
          </Link>
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
            <span>{itemTypeLabel[it.type]}</span>
            {it.published_version ? (
              <Badge tone="ok">Published {it.published_version.number}</Badge>
            ) : (
              <Badge tone="neutral">Not published</Badge>
            )}
            {it.draft_version && <Badge tone="accent">Draft {it.draft_version.number}</Badge>}
          </>
        }
      />
      <Tabs defaultValue="versions">
        <TabsList aria-label="Item sections">
          <TabsTrigger value="versions">Versions</TabsTrigger>
          <TabsTrigger value="definition">Definition</TabsTrigger>
          <TabsTrigger value="details">Details</TabsTrigger>
          <TabsTrigger value="entitlements">Entitlements</TabsTrigger>
        </TabsList>
        <TabsContent value="versions" className="pt-4">
          <VersionsTab item={it} />
        </TabsContent>
        <TabsContent value="definition" className="pt-4">
          <DefinitionTab item={it} />
        </TabsContent>
        <TabsContent value="details" className="pt-4">
          <DetailsTab key={it.id + it.name + it.icon} item={it} />
        </TabsContent>
        <TabsContent value="entitlements" className="pt-4">
          <EntitlementsTab key={JSON.stringify(it.entitlements)} item={it} />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
