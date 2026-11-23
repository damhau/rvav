import { keepPreviousData, type Query, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Package } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { GitBadge } from "@/features/admin/catalog/GitPanels";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemType } from "@/lib/api/types";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { draftCopy } from "./designer/draftStore";

export const itemKinds = ["vm_blueprint", "awx_job_template", "awx_workflow", "workflow", "composite"] as const;

/** The items list's filters, in the address beside the tab. */
export interface ItemsSearch {
  q?: string;
  category?: string;
  state?: "published" | "unpublished";
  kind?: (typeof itemKinds)[number];
}

const tr = i18n.getFixedT(null, "admin-catalog");
const yamlStart = "apiVersion: rvav.dev/v1";

/** Who may request an item, in words. */
export function openToText(it: AdminCatalogItem): string {
  const n = it.entitlements.project_ids.length;
  if (it.entitlements.all_projects) return tr("items.openTo.every");
  return n === 0 ? tr("items.openTo.none") : tr("items.openTo.count", { count: n });
}

export function ItemState({ it }: { it: AdminCatalogItem }) {
  const { t } = useTranslation("admin-catalog");
  return (
    <div className="flex flex-wrap gap-1">
      {it.published_version ? (
        <Badge tone="ok">{t("items.published", { number: it.published_version.number })}</Badge>
      ) : (
        <Badge tone="neutral">{t("items.notPublished")}</Badge>
      )}
      {it.draft_version && <Badge tone="accent">{t("items.draft", { number: it.draft_version.number })}</Badge>}
      {it.git && it.git.state !== "synced" && <GitBadge git={it.git} />}
    </div>
  );
}

function NewItemSheet({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
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
      toast.success(t("items.new.created", { name: it.name }));
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: it.slug } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("items.new.notCreated"));
    },
  });
  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={create.isPending}
      title={t("items.new.title")}
      description={t("items.new.description")}
      submitLabel={create.isPending ? t("items.new.creating") : t("items.new.create")}
      submitDisabled={!form.name || !form.slug || !form.summary}
      onSubmit={() => {
        setErrors({});
        create.mutate();
      }}
    >
      <Field label={t("items.new.name")} htmlFor="new-item-name" error={errors["/name"]}>
        <Input id="new-item-name" value={form.name} onChange={(e) => set("name")(e.target.value)} maxLength={80} />
      </Field>
      <Field label={t("items.new.slug")} htmlFor="new-item-slug" hint={t("items.new.slugHint")} error={errors["/slug"]}>
        <Input
          id="new-item-slug"
          value={form.slug}
          onChange={(e) => set("slug")(e.target.value)}
          className="font-mono"
        />
      </Field>
      <Field
        label={t("items.new.summary")}
        htmlFor="new-item-summary"
        hint={t("items.new.summaryHint")}
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
        <Field label={t("items.new.category")} htmlFor="new-item-category" error={errors["/category"]}>
          <Select
            id="new-item-category"
            value={form.category}
            onChange={set("category")}
            loading={categories.isPending}
            options={(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name }))}
          />
        </Field>
        <Field label={t("items.new.type")} htmlFor="new-item-type">
          <Select
            id="new-item-type"
            value={form.type}
            onChange={set("type")}
            options={itemKinds.map((ty) => ({ value: ty, label: itemTypeLabel(ty) }))}
          />
        </Field>
      </div>
    </Sheet>
  );
}

function ImportSheet({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
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
      toast.success(t("items.import.done"));
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: out.item_id } });
    },
    onError: (e) =>
      setProblems(
        isApiError(e) && e.fieldErrors.length
          ? e.fieldErrors.map((x) => x.message)
          : [isApiError(e) ? (e.problem.detail ?? e.problem.title) : t("items.import.failed")],
      ),
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={run.isPending}
      title={t("items.import.title")}
      description={t("items.import.description")}
      submitLabel={run.isPending ? t("items.import.importing") : t("items.import.import")}
      submitDisabled={!text.trim()}
      onSubmit={() => {
        setProblems([]);
        run.mutate();
      }}
    >
      <Field label={t("items.import.file")} htmlFor="import-file">
        <input
          id="import-file"
          type="file"
          accept=".yaml,.yml,application/yaml,text/yaml"
          className="text-secondary"
          onChange={async (e) => setText((await e.target.files?.[0]?.text()) ?? "")}
        />
      </Field>
      <Field label={t("items.import.yaml")} htmlFor="import-yaml">
        <Textarea
          id="import-yaml"
          className="min-h-80 font-mono text-secondary"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={yamlStart}
        />
      </Field>
      {problems.length > 0 && (
        <ul aria-label={t("items.import.problems")} className="flex flex-col gap-1 text-secondary text-bad">
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
  const { t } = useTranslation("admin-catalog");
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
        : call(
            api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", {
              params: { path },
              body: { withdraw: true },
            }),
          );
    },
    onSuccess: (_, { it, action }) => {
      // A deleted item's page isn't read again: it would show the item not found before it leaves.
      const gone = action === "delete" ? itemPageOf(it) : () => false;
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all, predicate: (query) => !gone(query) });
      void qc.invalidateQueries({ queryKey: keys.catalog.all });
      toast.success(
        {
          publish: t("items.toast.published", { name: it.name, number: it.draft_version?.number }),
          withdraw: t("items.toast.withdrawn", { name: it.name }),
          delete: t("items.toast.deleted", { name: it.name }),
        }[action],
      );
    },
    onError: (e) => failed(e, t("items.toast.failed")),
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
  const { t } = useTranslation("admin-catalog");
  const navigate = useNavigate();
  const { projectId } = useCurrentProject();
  const search: ItemsSearch = useSearch({ from: "/admin/catalog-items/" });
  const filters = useAddressFilters(search, ["q", "category", "state", "kind"]);
  const [sheet, setSheet] = useState<"new" | "import" | null>(null);
  const [asking, setAsking] = useState<Change | null>(null);
  const items = useQuery({
    ...q.adminCatalogItems({ q: search.q, category: search.category, state: search.state }),
    placeholderData: keepPreviousData,
  });
  const categories = useQuery(q.adminCatalogCategories());
  const change = useItemChange();
  const categoryName = (key: string) => categories.data?.items.find((c) => c.key === key)?.name ?? key;
  const rows = items.data?.items.filter((it) => !search.kind || it.type === search.kind);
  // An empty catalog offers New item in its empty state only: one primary action at a time.
  const listed = filters.filtered || Boolean(rows?.length);
  const requestable = (it: AdminCatalogItem) => requestableIn(it, projectId);

  const columns: Column<AdminCatalogItem>[] = [
    {
      id: "item",
      header: t("items.col.item"),
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
      header: t("items.col.kind"),
      sortValue: (it) => itemTypeLabel(it.type),
      cell: (it) => (
        <span className="text-secondary">
          {itemTypeLabel(it.type)}
          {it.source.kind === "awx" && <span className="block text-muted">{t("items.col.fromAwx")}</span>}
        </span>
      ),
    },
    {
      id: "category",
      header: t("items.col.category"),
      sortValue: (it) => categoryName(it.category),
      cell: (it) => <span className="text-secondary">{categoryName(it.category)}</span>,
    },
    { id: "state", header: t("items.col.state"), cell: (it) => <ItemState it={it} /> },
    {
      id: "open",
      header: t("items.col.openTo"),
      sortValue: (it) => openToText(it),
      cell: (it) => <span className="text-secondary text-muted">{openToText(it)}</span>,
    },
  ];

  const add = (
    <Button size="sm" variant="primary" onClick={() => setSheet("new")}>
      {t("items.newItem")}
    </Button>
  );
  return (
    <Panel
      title={t("items.title")}
      description={t("items.description")}
      flush
      actions={
        <>
          <Button size="sm" onClick={() => setSheet("import")}>
            {t("items.importYaml")}
          </Button>
          {listed && add}
        </>
      }
    >
      <DataTable
        label={t("items.title")}
        columns={columns}
        rows={rows}
        rowKey={(it) => it.id}
        rowLinks
        minWidth={820}
        loading={items.isPending}
        error={items.isError && <ErrorState error={items.error} onRetry={() => void items.refetch()} />}
        toolbar={
          <FilterBar filters={filters} search={{ label: t("items.search"), placeholder: t("items.searchPlaceholder") }}>
            <FilterSelect
              name="kind"
              label={t("items.kind")}
              any={t("items.allKinds")}
              options={itemKinds.map((ty) => ({ value: ty, label: itemTypeLabel(ty) }))}
              className="w-44"
            />
            <FilterSelect
              name="category"
              label={t("items.category")}
              any={t("items.allCategories")}
              options={(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name }))}
              className="w-44"
            />
            <FilterSelect
              name="state"
              label={t("items.state")}
              any={t("items.anyState")}
              options={[
                { value: "published", label: t("items.statePublished") },
                { value: "unpublished", label: t("items.stateUnpublished") },
              ]}
              className="w-40"
            />
          </FilterBar>
        }
        empty={
          filters.filtered ? (
            <FilterEmpty filters={filters} icon={Package} title={t("items.noMatch")} body={t("items.noMatchBody")} />
          ) : (
            <EmptyState icon={Package} title={t("items.empty")} body={t("items.emptyBody")} action={add} />
          )
        }
        rowMenuLabel={(it) => t("items.actionsFor", { name: it.name })}
        rowMenu={(it) => (
          <>
            <MenuItem
              onSelect={() => void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: it.slug } })}
            >
              {t("items.open")}
            </MenuItem>
            {requestable(it) && (
              <MenuItem onSelect={() => void navigate({ to: "/catalog/$itemId/request", params: { itemId: it.slug } })}>
                {t("items.openForm")}
              </MenuItem>
            )}
            {(it.draft_version || it.published_version) && <MenuSeparator />}
            {it.draft_version && (
              <MenuItem
                disabled={unsavedDraft(it) || change.isPending}
                onSelect={() => change.mutate({ it, action: "publish" })}
              >
                {unsavedDraft(it)
                  ? t("items.publishUnsaved")
                  : change.isPending
                    ? t("items.publishWaits", { number: it.draft_version.number })
                    : t("items.publish", { number: it.draft_version.number })}
              </MenuItem>
            )}
            {it.published_version && (
              <MenuItem onSelect={() => setAsking({ it, action: "withdraw" })}>{t("items.withdraw")}</MenuItem>
            )}
            {!it.requested && (
              <>
                <MenuSeparator />
                <MenuItem danger onSelect={() => setAsking({ it, action: "delete" })}>
                  {t("items.delete")}
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
  const { t } = useTranslation("admin-catalog");
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
      title={t("items.withdrawTitle", { name: it.name })}
      description={t("items.withdrawBody", { stays: staysText(it) })}
      consequences={withdrawConsequences(it)}
      confirmLabel={t("items.withdraw")}
      pending={run.isPending}
      onConfirm={confirm}
    />
  ) : (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("items.deleteTitle", { name: it.name })}
      description={t("items.deleteBody", { count: n })}
      consequences={t(it.published_version ? "items.deleteConsequencesPublished" : "items.deleteConsequences", {
        openTo: openToText(it).toLowerCase(),
      })}
      confirmLabel={t("items.deleteItem")}
      pending={run.isPending}
      onConfirm={confirm}
    />
  );
}

/** The versions a withdrawal retires, kept to roll back to. */
function staysText(it: AdminCatalogItem): string {
  const offered = it.versions.filter((v) => v.state === "published").map((v) => v.number);
  return offered.length > 1
    ? tr("items.staysMany", { versions: formatList(offered) })
    : tr("items.staysOne", { number: it.published_version?.number });
}

/** What a withdrawal leaves: the deployments built from the version, and who loses it. */
function withdrawConsequences(it: AdminCatalogItem): string {
  const offered = it.versions.filter((v) => v.state === "published");
  const n = offered.reduce((sum, v) => sum + v.deployments, 0);
  const many = offered.length > 1;
  const deployments =
    n === 0
      ? tr(many ? "items.builtNoneMany" : "items.builtNoneOne")
      : tr(many ? "items.builtMany" : "items.builtOne", { count: n });
  return tr("items.leaves", { deployments, openTo: openToText(it).toLowerCase() });
}
