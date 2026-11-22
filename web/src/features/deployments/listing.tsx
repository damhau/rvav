// What the deployments and Resources lists share: the scope (the project
// or all of the person's), the layout each person last chose, and saved
// views, which keep a list's address query under a name.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bookmark, BookmarkPlus, Check, ChevronDown, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, Input } from "@/components/ui/input";
import { Segmented } from "@/components/ui/segmented";
import { api, call, isApiError } from "@/lib/api/client";
import type { SavedView, ViewList } from "@/lib/api/types";
import { keys, q } from "@/lib/query";

/** A list's address query, each value as text: what a saved view keeps. */
export type ListQuery = Record<string, string>;

export function toQuery(search: object): ListQuery {
  const out: ListQuery = {};
  for (const [k, v] of Object.entries(search)) {
    if (v !== undefined && v !== null && v !== "" && v !== false) out[k] = String(v);
  }
  return out;
}

export function sameQuery(a: ListQuery, b: ListQuery) {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => a[k] === b[k]);
}

export type Layout = "table" | "cards";

const layoutKey = (list: ViewList, userId: string) => `rvav.layout.${list}.${userId}`;

/** The layout a person last chose for a list, on this browser. */
export function rememberedLayout(list: ViewList, userId: string): Layout {
  try {
    return globalThis.localStorage?.getItem(layoutKey(list, userId)) === "cards" ? "cards" : "table";
  } catch {
    return "table";
  }
}

export function rememberLayout(list: ViewList, userId: string, layout: Layout) {
  try {
    globalThis.localStorage?.setItem(layoutKey(list, userId), layout);
  } catch {
    // Not remembered when storage is blocked: the address still holds it.
  }
}

/** The project the person works in, or all of theirs. */
export function ScopeToggle({
  all,
  projectName,
  onChange,
}: {
  all: boolean;
  projectName: string;
  onChange: (all: boolean) => void;
}) {
  const { t } = useTranslation("deployments");
  return (
    <Segmented
      label={t("views.projects")}
      value={all ? "all" : "one"}
      onChange={(v) => onChange(v === "all")}
      options={[
        { value: "one", label: projectName },
        { value: "all", label: t("views.allProjects") },
      ]}
    />
  );
}

/**
 * The person's saved views of a list: one applies its query; the menu saves
 * the list's query as a new view, updates the view last applied, and renames
 * or deletes the one shown.
 */
export function SavedViewsMenu({
  list,
  query,
  onApply,
}: {
  list: ViewList;
  query: ListQuery;
  onApply: (query: ListQuery) => void;
}) {
  const { t } = useTranslation("deployments");
  const qc = useQueryClient();
  const views = useQuery(q.savedViews(list));
  const [last, setLast] = useState<string | null>(null);
  const [open, setOpen] = useState<{ kind: "save" | "rename" | "delete"; view?: SavedView } | null>(null);
  const items = views.data?.items ?? [];
  const shown = items.find((v) => sameQuery(v.query, query));
  const lastApplied = items.find((v) => v.id === last);
  const refresh = () => void qc.invalidateQueries({ queryKey: keys.views(list) });

  const update = useMutation({
    mutationFn: (v: SavedView) =>
      call(api.PUT("/me/views/{id}", { params: { path: { id: v.id } }, body: { list, name: v.name, query } })),
    onSuccess: (v) => {
      refresh();
      toast.success(t("views.updated", { name: v.name }));
    },
    onError: (e) => toast.error(isApiError(e) ? e.message : t("views.notSaved")),
  });
  const remove = useMutation({
    mutationFn: (v: SavedView) => call(api.DELETE("/me/views/{id}", { params: { path: { id: v.id } } })),
    onSuccess: () => {
      refresh();
      setOpen(null);
      toast.success(t("views.deleted"));
    },
    onError: (e) => toast.error(isApiError(e) ? e.message : t("views.notDeleted")),
  });

  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button>
            <Bookmark />
            {shown ? (
              <>
                <span className="sr-only">{t("views.savedView")}</span>
                <span className="max-w-40 truncate">{shown.name}</span>
              </>
            ) : (
              t("views.views")
            )}
            <ChevronDown className="text-faint" />
          </Button>
        </MenuTrigger>
        <MenuContent align="end" className="max-w-80">
          <MenuLabel>{t("views.yours")}</MenuLabel>
          {items.length === 0 && <p className="px-2 pb-1.5 text-secondary text-muted">{t("views.none")}</p>}
          {items.map((v) => (
            <MenuItem
              key={v.id}
              onSelect={() => {
                setLast(v.id);
                onApply(v.query);
              }}
            >
              <span className="truncate">{v.name}</span>
              {shown?.id === v.id && <Check className="ml-auto text-accent!" aria-label={t("views.shown")} />}
            </MenuItem>
          ))}
          <MenuSeparator />
          <MenuItem onSelect={() => setOpen({ kind: "save" })}>
            <BookmarkPlus /> {t("views.saveNew")}
          </MenuItem>
          {lastApplied && !shown && (
            <MenuItem onSelect={() => update.mutate(lastApplied)}>
              <RefreshCw /> {t("views.update", { name: lastApplied.name })}
            </MenuItem>
          )}
          {shown && (
            <MenuItem onSelect={() => setOpen({ kind: "rename", view: shown })}>
              <Pencil /> {t("views.rename", { name: shown.name })}
            </MenuItem>
          )}
          {shown && (
            <MenuItem danger onSelect={() => setOpen({ kind: "delete", view: shown })}>
              <Trash2 /> {t("views.delete", { name: shown.name })}
            </MenuItem>
          )}
        </MenuContent>
      </Menu>
      {(open?.kind === "save" || open?.kind === "rename") && (
        <ViewNameDialog
          list={list}
          query={open.view?.query ?? query}
          view={open.view}
          onClose={() => setOpen(null)}
          onSaved={(v) => {
            refresh();
            setLast(v.id);
            setOpen(null);
            toast.success(open.view ? t("views.renamed", { name: v.name }) : t("views.saved", { name: v.name }));
          }}
        />
      )}
      {open?.kind === "delete" && open.view && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setOpen(null)}
          title={t("views.deleteTitle", { name: open.view.name })}
          description={t("views.deleteBody")}
          confirmLabel={t("views.deleteConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(open.view!)}
        />
      )}
    </>
  );
}

function ViewNameDialog({
  list,
  query,
  view,
  onClose,
  onSaved,
}: {
  list: ViewList;
  query: ListQuery;
  view?: SavedView;
  onClose: () => void;
  onSaved: (v: SavedView) => void;
}) {
  const { t } = useTranslation(["deployments", "common"]);
  const [name, setName] = useState(view?.name ?? "");
  const save = useMutation({
    mutationFn: () => {
      const body = { list, name: name.trim(), query };
      return view
        ? call(api.PUT("/me/views/{id}", { params: { path: { id: view.id } }, body }))
        : call(api.POST("/me/views", { body }));
    },
    onSuccess: onSaved,
  });
  const problem = save.error
    ? isApiError(save.error)
      ? (save.error.fieldErrors.find((f) => f.field === "/name")?.message ?? save.error.message)
      : t("views.notSavedRetry")
    : undefined;
  return (
    <Dialog open onOpenChange={(o) => !o && !save.isPending && onClose()}>
      <DialogContent
        title={view ? t("views.renameTitle", { name: view.name }) : t("views.saveTitle")}
        description={view ? undefined : t("views.saveBody")}
        onOpenAutoFocus={(e) => {
          // The name it asks for, rather than Close.
          e.preventDefault();
          document.getElementById("view-name")?.focus();
        }}
      >
        <form
          className="flex flex-col gap-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) save.mutate();
          }}
        >
          <Field label={t("views.name")} htmlFor="view-name" error={problem}>
            <Input
              id="view-name"
              value={name}
              maxLength={60}
              autoComplete="off"
              onChange={(e) => setName(e.target.value)}
              placeholder={t("views.namePlaceholder")}
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="quiet" onClick={onClose} disabled={save.isPending}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" variant="primary" disabled={!name.trim() || save.isPending}>
              {save.isPending ? t("views.saving") : view ? t("views.renameView") : t("views.saveView")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
