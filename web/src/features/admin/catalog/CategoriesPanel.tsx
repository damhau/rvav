import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Shapes } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogCategory } from "@/lib/api/types";
import { keys, q } from "@/lib/query";

// A key, the same in every language.
const keyExample = "storage";

/** The catalog's own category list (cached for minutes) changes too. */
function useRefresh() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
    void qc.invalidateQueries({ queryKey: keys.catalog.all });
  };
}

function CategorySheet({
  existing,
  next,
  onClose,
}: {
  existing?: AdminCatalogCategory;
  /** The order a new category gets: after the last one. */
  next: number;
  onClose: () => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const refresh = useRefresh();
  const [key, setKey] = useState(existing?.key ?? "");
  const [name, setName] = useState(existing?.name ?? "");
  const [order, setOrder] = useState(String(existing?.sort_order ?? next));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const sort_order = Number(order) || 0;
      return existing
        ? call(
            api.PATCH("/admin/catalog/categories/{key}", {
              params: { path: { key: existing.key } },
              body: { name, sort_order },
            }),
          )
        : call(api.POST("/admin/catalog/categories", { body: { key, name, sort_order } }));
    },
    onSuccess: () => {
      refresh();
      toast.success(existing ? t("categories.saved", { name }) : t("categories.created", { name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("categories.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? t("categories.change", { name: existing.name }) : t("categories.new")}
      description={t("categories.sheetDescription")}
      submitLabel={save.isPending ? t("categories.saving") : existing ? t("categories.save") : t("categories.create")}
      submitDisabled={!key || !name}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label={t("categories.key")} htmlFor="category-key" hint={t("categories.keyHint")} error={errors["/key"]}>
        <Input
          id="category-key"
          className="font-mono"
          value={key}
          disabled={Boolean(existing)}
          onChange={(e) => setKey(e.target.value)}
          placeholder={keyExample}
        />
      </Field>
      <Field
        label={t("categories.name")}
        htmlFor="category-name"
        hint={t("categories.nameHint")}
        error={errors["/name"]}
      >
        <Input id="category-name" value={name} maxLength={60} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field
        label={t("categories.order")}
        htmlFor="category-order"
        hint={t("categories.orderHint")}
        error={errors["/sort_order"]}
      >
        <Input
          id="category-order"
          type="number"
          className="w-24"
          value={order}
          onChange={(e) => setOrder(e.target.value)}
        />
      </Field>
    </Sheet>
  );
}

export function CategoriesPanel() {
  const { t } = useTranslation("admin-catalog");
  const refresh = useRefresh();
  const categories = useQuery(q.adminCatalogCategories());
  const [editing, setEditing] = useState<AdminCatalogCategory | "new" | null>(null);
  const [removing, setRemoving] = useState<AdminCatalogCategory | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/catalog/categories/{key}", { params: { path: { key } } })),
    onSuccess: () => {
      refresh();
      toast.success(t("categories.removed", { name: removing?.name ?? t("categories.theCategory") }));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("categories.notRemoved")),
  });
  const list = categories.data?.items;
  const columns: Column<AdminCatalogCategory>[] = [
    {
      id: "order",
      header: t("categories.order"),
      sortValue: (c) => c.sort_order ?? 0,
      cell: (c) => <span className="text-muted tabular-nums">{c.sort_order ?? 0}</span>,
      className: "w-20",
    },
    {
      id: "name",
      header: t("categories.name"),
      sortValue: (c) => c.name,
      cell: (c) => <span className="font-medium">{c.name}</span>,
    },
    {
      id: "key",
      header: t("categories.key"),
      sortValue: (c) => c.key,
      cell: (c) => <span className="font-mono text-secondary text-muted">{c.key}</span>,
    },
    {
      id: "items",
      header: t("categories.items"),
      align: "end",
      sortValue: (c) => c.items ?? 0,
      cell: (c) => <span className="tabular-nums">{c.items ?? 0}</span>,
    },
  ];
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("categories.new")}
    </Button>
  );
  const next = Math.max(0, ...(list ?? []).map((c) => c.sort_order ?? 0)) + 1;
  return (
    <Panel
      title={t("categories.title")}
      description={t("categories.description")}
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label={t("categories.title")}
        columns={columns}
        rows={list}
        rowKey={(c) => c.key}
        minWidth={520}
        loading={categories.isPending}
        error={categories.isError && <ErrorState error={categories.error} onRetry={() => void categories.refetch()} />}
        empty={<EmptyState icon={Shapes} title={t("categories.empty")} body={t("categories.emptyBody")} action={add} />}
        rowMenuLabel={(c) => t("categories.actionsFor", { name: c.name })}
        rowMenu={(c) => (
          <>
            <MenuItem onSelect={() => setEditing(c)}>{t("categories.changeAction")}</MenuItem>
            <MenuSeparator />
            <MenuItem danger disabled={(c.items ?? 0) > 0} onSelect={() => setRemoving(c)}>
              {(c.items ?? 0) > 0 ? t("categories.removeFirst") : t("categories.remove")}
            </MenuItem>
          </>
        )}
      />
      {editing && (
        <CategorySheet
          existing={editing === "new" ? undefined : editing}
          next={next}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("categories.removeTitle", { name: removing.name })}
          description={t("categories.removeBody")}
          consequences={t("categories.removeConsequences")}
          confirmLabel={t("categories.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.key)}
        />
      )}
    </Panel>
  );
}
