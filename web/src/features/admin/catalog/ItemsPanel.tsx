import { keepPreviousData, type Query, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Package } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, TableSearch, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { GitBadge } from "@/features/admin/catalog/GitPanels";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemType } from "@/lib/api/types";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { keys, q, type AdminItemFilters } from "@/lib/query";
import { draftCopy } from "./designer/draftStore";

const types = ["vm_blueprint", "awx_job_template", "awx_workflow", "workflow", "composite"] as const;

/** Who may request an item, in words. */
export function openToText(it: AdminCatalogItem): string {
  const n = it.entitlements.project_ids.length;
  if (it.entitlements.all_projects) return "Every project";
  return n === 0 ? "No project" : `${n} project${n > 1 ? "s" : ""}`;
}

export function ItemState({ it }: { it: AdminCatalogItem }) {
  return (
    <div className="flex flex-wrap gap-1">
      {it.published_version ? (
        <Badge tone="ok">Published {it.published_version.number}</Badge>
      ) : (
        <Badge tone="neutral">Not published</Badge>
      )}
      {it.draft_version && <Badge tone="accent">Draft {it.draft_version.number}</Badge>}
      {it.git && it.git.state !== "synced" && <GitBadge git={it.git} />}
    </div>
  );
}

function NewItemSheet({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const categories = useQuery(q.adminCatalogCategories());
  const [form, setForm] = useState({
    slug: "",
    name: "",
    summary: "",
    category: "compute",
    type: "vm_blueprint" as CatalogItemType,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/items", { body: form })),
    onSuccess: (it) => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(`${it.name} created, with a draft 1.0`);
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: it.slug } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The item wasn't created");
    },
  });
  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={create.isPending}
      title="New item"
      description="It starts as a draft 1.0; publish it when its form and blueprint are ready."
      submitLabel={create.isPending ? "Creating…" : "Create"}
      submitDisabled={!form.name || !form.slug || !form.summary}
      onSubmit={() => {
        setErrors({});
        create.mutate();
      }}
    >
      <Field label="Name" htmlFor="new-item-name" error={errors["/name"]}>
        <Input id="new-item-name" value={form.name} onChange={(e) => set("name")(e.target.value)} maxLength={80} />
      </Field>
      <Field
        label="Slug"
        htmlFor="new-item-slug"
        hint="In URLs and YAML: lower case, digits and dashes. It can't change later."
        error={errors["/slug"]}
      >
        <Input
          id="new-item-slug"
          value={form.slug}
          onChange={(e) => set("slug")(e.target.value)}
          className="font-mono"
        />
      </Field>
      <Field
        label="Summary"
        htmlFor="new-item-summary"
        hint="One line on the catalog's card."
        error={errors["/summary"]}
      >
        <Textarea
          id="new-item-summary"
          value={form.summary}
          onChange={(e) => set("summary")(e.target.value)}
          maxLength={200}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Category" htmlFor="new-item-category" error={errors["/category"]}>
          <Select
            id="new-item-category"
            value={form.category}
            onChange={set("category")}
            loading={categories.isPending}
            options={(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name }))}
          />
        </Field>
        <Field label="Type" htmlFor="new-item-type">
          <Select
            id="new-item-type"
            value={form.type}
            onChange={set("type")}
            options={types.map((t) => ({ value: t, label: itemTypeLabel[t] }))}
          />
        </Field>
      </div>
    </Sheet>
  );
}

function ImportSheet({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [problems, setProblems] = useState<string[]>([]);
  const run = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/catalog/import", {
          body: text,
          bodySerializer: (b: string) => b,
          headers: { "Content-Type": "application/yaml" },
        }),
      ),
    onSuccess: (out) => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success("Imported as a draft");
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: out.item_id } });
    },
    onError: (e) =>
      setProblems(
        isApiError(e) && e.fieldErrors.length
          ? e.fieldErrors.map((x) => x.message)
          : [isApiError(e) ? (e.problem.detail ?? e.problem.title) : "The import failed."],
      ),
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={run.isPending}
      title="Import an item"
      description="Paste or open an item exported as YAML. It comes in as a draft; nothing is published."
      submitLabel={run.isPending ? "Importing…" : "Import"}
      submitDisabled={!text.trim()}
      onSubmit={() => {
        setProblems([]);
        run.mutate();
      }}
    >
      <Field label="YAML file" htmlFor="import-file">
        <input
          id="import-file"
          type="file"
          accept=".yaml,.yml,application/yaml,text/yaml"
          className="text-secondary"
          onChange={async (e) => setText((await e.target.files?.[0]?.text()) ?? "")}
        />
      </Field>
      <Field label="YAML" htmlFor="import-yaml">
        <Textarea
          id="import-yaml"
          className="min-h-80 font-mono text-secondary"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="apiVersion: rvav.dev/v1"
        />
      </Field>
      {problems.length > 0 && (
        <ul aria-label="Import problems" className="flex flex-col gap-1 text-secondary text-bad">
          {problems.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

type Change = { it: AdminCatalogItem; action: "publish" | "withdraw" | "delete" };

/** The item's own page, by its ID or its slug. */
const itemPageOf = (it: AdminCatalogItem) => (query: Query) => {
  const [a, b, c, id] = query.queryKey;
  return a === "admin" && b === "catalog" && c === "item" && (id === it.id || id === it.slug);
};

function useItemChange() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ it, action }: Change) => {
      const itemId = it.id;
      if (action === "delete")
        return call(api.DELETE("/admin/catalog/items/{itemId}", { params: { path: { itemId } } }));
      const v = action === "publish" ? it.draft_version! : it.published_version!;
      const path = { itemId, versionId: v.id };
      return action === "publish"
        ? call(api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", { params: { path }, body: {} }))
        : call(api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", { params: { path } }));
    },
    onSuccess: (_, { it, action }) => {
      // A deleted item's page isn't read again: it would show the item not found before it leaves.
      const gone = action === "delete" ? itemPageOf(it) : () => false;
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all, predicate: (query) => !gone(query) });
      void qc.invalidateQueries({ queryKey: keys.catalog.all });
      toast.success(
        {
          publish: `${it.name} ${it.draft_version?.number} published`,
          withdraw: `${it.name} withdrawn`,
          delete: `${it.name} deleted`,
        }[action],
      );
    },
    onError: (e) => failed(e, "That didn't work"),
  });
}

/** The form designer holds changes to the draft the server hasn't taken yet: publishing waits for them. */
function unsavedDraft(it: AdminCatalogItem): boolean {
  return Boolean(it.draft_version && draftCopy(it.draft_version.id)?.dirty);
}

/** The item has a request form for the project you are in. */
export function requestableIn(it: AdminCatalogItem, projectId: string | undefined): boolean {
  return (
    Boolean(it.published_version) &&
    (it.entitlements.all_projects || (projectId !== undefined && it.entitlements.project_ids.includes(projectId)))
  );
}

export function ItemsPanel() {
  const navigate = useNavigate();
  const { projectId } = useCurrentProject();
  const [filters, setFilters] = useState<AdminItemFilters>({});
  const [kind, setKind] = useState<CatalogItemType | "any">("any");
  const [sheet, setSheet] = useState<"new" | "import" | null>(null);
  const [asking, setAsking] = useState<Change | null>(null);
  const [text, setText] = useSearchBox(filters.q, (next) => setFilters((f) => ({ ...f, q: next })));
  const items = useQuery({ ...q.adminCatalogItems(filters), placeholderData: keepPreviousData });
  const categories = useQuery(q.adminCatalogCategories());
  const change = useItemChange();
  const categoryName = (key: string) => categories.data?.items.find((c) => c.key === key)?.name ?? key;
  const rows = items.data?.items.filter((it) => kind === "any" || it.type === kind);
  const filtered = Boolean(filters.q || filters.category || filters.state || kind !== "any");
  // An empty catalog offers New item in its empty state only: one primary action at a time.
  const listed = filtered || Boolean(rows?.length);
  const requestable = (it: AdminCatalogItem) => requestableIn(it, projectId);

  const columns: Column<AdminCatalogItem>[] = [
    {
      id: "item",
      header: "Item",
      sortValue: (it) => it.name,
      cell: (it) => (
        <div className="flex items-center gap-2.5">
          <ItemIcon name={it.icon} />
          <div className="min-w-0">
            <Link
              to="/admin/catalog-items/$itemId"
              params={{ itemId: it.slug }}
              className="font-medium text-fg hover:text-accent"
            >
              {it.name}
            </Link>
            <div className="font-mono text-caption text-muted">{it.slug}</div>
          </div>
        </div>
      ),
    },
    {
      id: "kind",
      header: "Kind",
      sortValue: (it) => itemTypeLabel[it.type],
      cell: (it) => (
        <span className="text-secondary">
          {itemTypeLabel[it.type]}
          {it.source.kind === "awx" && <span className="block text-muted">from AWX</span>}
        </span>
      ),
    },
    {
      id: "category",
      header: "Category",
      sortValue: (it) => categoryName(it.category),
      cell: (it) => <span className="text-secondary">{categoryName(it.category)}</span>,
    },
    { id: "state", header: "State", cell: (it) => <ItemState it={it} /> },
    {
      id: "open",
      header: "Open to",
      sortValue: (it) => openToText(it),
      cell: (it) => <span className="text-secondary text-muted">{openToText(it)}</span>,
    },
  ];

  const add = (
    <Button size="sm" variant="primary" onClick={() => setSheet("new")}>
      New item
    </Button>
  );
  return (
    <Panel
      title="Items"
      description="What people can request, and the version of each they get."
      flush
      actions={
        <>
          <Button size="sm" onClick={() => setSheet("import")}>
            Import YAML
          </Button>
          {listed && add}
        </>
      }
    >
      <DataTable
        label="Items"
        columns={columns}
        rows={rows}
        rowKey={(it) => it.id}
        rowLinks
        minWidth={820}
        loading={items.isPending}
        error={items.isError && <ErrorState error={items.error} onRetry={() => void items.refetch()} />}
        toolbar={
          <>
            <TableSearch label="Search items" placeholder="Search by name or slug" value={text} onChange={setText} />
            <Select
              aria-label="Kind"
              value={kind}
              onChange={(v) => setKind(v as CatalogItemType | "any")}
              options={[
                { value: "any", label: "All kinds" },
                ...types.map((t) => ({ value: t, label: itemTypeLabel[t] })),
              ]}
              className="w-44"
            />
            <Select
              aria-label="Category"
              value={filters.category ?? "any"}
              onChange={(v) => setFilters((f) => ({ ...f, category: v === "any" ? undefined : v }))}
              options={[
                { value: "any", label: "All categories" },
                ...(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name })),
              ]}
              className="w-44"
            />
            <Select
              aria-label="State"
              value={filters.state ?? "any"}
              onChange={(v) =>
                setFilters((f) => ({ ...f, state: v === "any" ? undefined : (v as AdminItemFilters["state"]) }))
              }
              options={[
                { value: "any", label: "Any state" },
                { value: "published", label: "Published" },
                { value: "unpublished", label: "Not published" },
              ]}
              className="w-40"
            />
          </>
        }
        empty={
          filtered ? (
            <EmptyState
              icon={Package}
              title="No item matches"
              body="Nothing in the catalog has this name, kind, category and state."
              action={
                <Button
                  size="sm"
                  onClick={() => {
                    setFilters({});
                    setKind("any");
                  }}
                >
                  Clear the filters
                </Button>
              }
            />
          ) : (
            <EmptyState
              icon={Package}
              title="No items yet"
              body="An item is what people request: a VM, an AWX job, a workflow, or several of them together."
              action={add}
            />
          )
        }
        rowMenuLabel={(it) => `Actions for ${it.name}`}
        rowMenu={(it) => (
          <>
            <MenuItem
              onSelect={() => void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: it.slug } })}
            >
              Open
            </MenuItem>
            {requestable(it) && (
              <MenuItem onSelect={() => void navigate({ to: "/catalog/$itemId/request", params: { itemId: it.slug } })}>
                Open the request form
              </MenuItem>
            )}
            {(it.draft_version || it.published_version) && <MenuSeparator />}
            {it.draft_version && (
              <MenuItem
                disabled={unsavedDraft(it) || change.isPending}
                onSelect={() => change.mutate({ it, action: "publish" })}
              >
                {unsavedDraft(it)
                  ? "Publish: the designer has changes to save"
                  : change.isPending
                    ? `Publish ${it.draft_version.number}: waits for the change running`
                    : `Publish ${it.draft_version.number}`}
              </MenuItem>
            )}
            {it.published_version && (
              <MenuItem onSelect={() => setAsking({ it, action: "withdraw" })}>Withdraw</MenuItem>
            )}
            {!it.requested && (
              <>
                <MenuSeparator />
                <MenuItem danger onSelect={() => setAsking({ it, action: "delete" })}>
                  Delete
                </MenuItem>
              </>
            )}
          </>
        )}
      />
      {sheet === "new" && <NewItemSheet onClose={() => setSheet(null)} />}
      {sheet === "import" && <ImportSheet onClose={() => setSheet(null)} />}
      {asking && asking.action !== "publish" && (
        <ItemChangeDialog change={{ it: asking.it, action: asking.action }} onClose={() => setAsking(null)} />
      )}
    </Panel>
  );
}

/** Asks before an item is withdrawn or deleted, saying what depends on it. */
export function ItemChangeDialog({
  change,
  onClose,
  onDone,
}: {
  change: { it: AdminCatalogItem; action: "withdraw" | "delete" };
  onClose: () => void;
  /** Called once it is done; the deleted item's page is forgotten once what it returns settles. */
  onDone?: () => Promise<unknown> | void;
}) {
  const qc = useQueryClient();
  const run = useItemChange();
  const { it, action } = change;
  const n = it.versions.length;
  const confirm = () =>
    run.mutate(change, {
      onSuccess: () => {
        onClose();
        void Promise.resolve(onDone?.()).then(
          () => action === "delete" && qc.removeQueries({ predicate: itemPageOf(it) }),
        );
      },
    });
  return action === "withdraw" ? (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Withdraw ${it.name}?`}
      description={`Nobody can request it until a version is published again. Version ${it.published_version?.number} stays, to roll back to.`}
      consequences={withdrawConsequences(it)}
      confirmLabel="Withdraw"
      pending={run.isPending}
      onConfirm={confirm}
    />
  ) : (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Delete ${it.name}?`}
      description={`The item and its ${n} version${n === 1 ? "" : "s"} are deleted. This can't be undone.`}
      consequences={`Nobody has requested it, so no deployment depends on it. It is open to ${openToText(it).toLowerCase()}${it.published_version ? ", and leaves their catalog" : ""}.`}
      confirmLabel="Delete item"
      pending={run.isPending}
      onConfirm={confirm}
    />
  );
}

/** What a withdrawal leaves: the deployments built from the version, and who loses it. */
function withdrawConsequences(it: AdminCatalogItem): string {
  const n = it.published_version?.deployments ?? 0;
  const deployments =
    n === 0
      ? "No deployment was built from it."
      : `The ${n} deployment${n === 1 ? "" : "s"} built from it keep${n === 1 ? "s" : ""} running, with their day-2 actions.`;
  return `${deployments} It leaves the catalog of ${openToText(it).toLowerCase()}.`;
}
