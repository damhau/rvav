import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Search, Star } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { itemTypeLabel } from "@/components/common/misc";
import { EmptyState, Page, PageHeader } from "@/components/common/page";
import { Input } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { CatalogItem, CatalogItemPage } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, roughDuration } from "@/lib/utils";

export interface CatalogSearch {
  category?: string;
  q?: string;
}

const glyph = (i: CatalogItem) => (i.type === "vm_blueprint" ? "VM" : i.type === "composite" ? "SET" : "AWX");

const approvalText = { never: "No approval", conditional: "Approval in production", always: "Needs approval" } as const;

function FavouriteButton({ item }: { item: CatalogItem }) {
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: (fav: boolean) =>
      call(
        fav
          ? api.PUT("/catalog/items/{itemId}/favourite", { params: { path: { itemId: item.id } } })
          : api.DELETE("/catalog/items/{itemId}/favourite", { params: { path: { itemId: item.id } } }),
      ),
    onMutate: async (fav) => {
      await qc.cancelQueries({ queryKey: keys.catalog.lists() });
      const snapshot = qc.getQueriesData<CatalogItemPage>({ queryKey: keys.catalog.lists() });
      qc.setQueriesData<CatalogItemPage>({ queryKey: keys.catalog.lists() }, (old) =>
        old ? { ...old, items: old.items.map((i) => (i.id === item.id ? { ...i, favourite: fav } : i)) } : old,
      );
      return { snapshot };
    },
    onError: (e, _fav, ctx) => {
      ctx?.snapshot.forEach(([key, data]) => qc.setQueryData(key, data));
      toast.error("Couldn't update your favourites", {
        description: isApiError(e) ? `Request ID ${e.requestId}` : undefined,
      });
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: keys.catalog.lists() }),
  });
  return (
    <button
      type="button"
      aria-pressed={item.favourite}
      aria-label={item.favourite ? `Remove ${item.name} from favourites` : `Add ${item.name} to favourites`}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        mutation.mutate(!item.favourite);
      }}
      className="relative z-10 -m-1 rounded-sm p-1 text-faint hover:text-warn"
    >
      <Star className={cn("size-3.5", item.favourite && "fill-warn text-warn")} aria-hidden />
    </button>
  );
}

function Card({ item }: { item: CatalogItem }) {
  return (
    <div className="relative flex flex-col gap-2.5 rounded-lg border border-line bg-surface p-3.5 transition-colors hover:border-line-strong">
      <div className="flex items-start gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line bg-sunken font-mono text-[11px] font-medium text-muted">
          {glyph(item)}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-px">
          <Link
            to="/catalog/$itemId/request"
            params={{ itemId: item.slug }}
            className="font-semibold text-fg after:absolute after:inset-0 after:content-[''] hover:text-fg"
          >
            {item.name}
          </Link>
          <span className="text-[11px] text-faint">
            {itemTypeLabel[item.type]}
            {item.source.awx_template_name ? ` · ${item.source.awx_template_name}` : ""}
          </span>
        </div>
        <FavouriteButton item={item} />
      </div>
      <p className="m-0 min-h-9 text-xs text-muted">{item.summary}</p>
      <div className="flex flex-wrap items-center gap-1.5 border-t border-line pt-2.5 text-[11px] text-faint">
        <span className="font-mono">
          {item.version.number.startsWith("AWX") ? item.version.number : `v${item.version.number}`}
        </span>
        {item.typical_duration_seconds ? (
          <>
            <span aria-hidden>·</span>
            <span>{roughDuration(item.typical_duration_seconds)}</span>
          </>
        ) : null}
        <span
          className={cn(
            "ml-auto",
            item.approval !== "never" && "rounded-sm bg-warn-soft px-1.5 py-px font-medium text-warn",
          )}
        >
          {approvalText[item.approval]}
        </span>
      </div>
    </div>
  );
}

export function CatalogPage({ search }: { search: CatalogSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/catalog/" });
  const [text, setText] = useState(search.q ?? "");
  const categories = useQuery(q.categories(projectId));
  const items = useQuery(q.catalog({ projectId, category: search.category, q: search.q }));

  // Keep the URL in step with the search box, without a history entry per keystroke.
  useEffect(() => {
    const t = setTimeout(() => {
      const next = text.trim() || undefined;
      if (next !== search.q) void navigate({ search: (s: CatalogSearch) => ({ ...s, q: next }), replace: true });
    }, 250);
    return () => clearTimeout(t);
  }, [text, search.q, navigate]);

  const total = categories.data?.items.reduce((n, c) => n + c.item_count, 0);
  const cats = [{ key: undefined, name: "All", item_count: total ?? 0 }, ...(categories.data?.items ?? [])];

  return (
    <Page>
      <PageHeader
        title="Catalog"
        meta={`Items shared with ${project.name}. VM blueprints run in rvav; AWX items launch job templates on awx.corp.example.`}
        actions={
          <span className="relative flex items-center">
            <Search className="pointer-events-none absolute left-2.5 size-3.5 text-faint" aria-hidden />
            <label htmlFor="catalog-search" className="sr-only">
              Filter the catalog
            </label>
            <Input
              id="catalog-search"
              type="search"
              placeholder="Filter items"
              value={text}
              onChange={(e) => setText(e.target.value)}
              className="w-60 max-w-[60vw] pl-8"
            />
          </span>
        }
      />
      <div className="flex flex-wrap items-start gap-5">
        <div role="group" aria-label="Categories" className="flex max-w-full flex-[1_1_180px] flex-wrap gap-0.5">
          <span className="label basis-full px-2 pb-1">Category</span>
          {categories.isPending
            ? [0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-7 basis-full" />)
            : cats.map((c) => {
                const active = search.category === c.key;
                return (
                  <Link
                    key={c.key ?? "all"}
                    to="/catalog"
                    search={(s: CatalogSearch) => ({ ...s, category: c.key })}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "flex h-[30px] flex-[1_1_160px] items-center justify-between gap-2 rounded-md px-2 text-muted hover:bg-sunken hover:text-fg",
                      active && "bg-surface font-medium text-fg shadow-[0_0_0_1px_var(--line)] hover:bg-surface",
                    )}
                  >
                    <span>{c.name}</span>
                    <span className="font-mono text-[11px] text-faint">{c.item_count}</span>
                  </Link>
                );
              })}
        </div>
        <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-2.5">
          {items.isPending ? (
            <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,290px),1fr))] gap-3">
              {[0, 1, 2, 3, 4, 5].map((i) => (
                <Skeleton key={i} className="h-36 rounded-lg" />
              ))}
            </div>
          ) : items.isError ? (
            <ErrorState
              error={items.error}
              onRetry={() => void items.refetch()}
              className="rounded-lg border border-line bg-surface"
            />
          ) : items.data.items.length === 0 ? (
            <div className="rounded-lg border border-line bg-surface">
              <EmptyState
                title="No items match"
                body="Clear the filter or pick another category. Ask a catalog admin if something you need is missing."
              />
            </div>
          ) : (
            <>
              <span className="text-xs text-muted" aria-live="polite">
                {items.data.items.length} {items.data.items.length === 1 ? "item" : "items"}
              </span>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,290px),1fr))] gap-3">
                {items.data.items.map((i) => (
                  <Card key={i.id} item={i} />
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </Page>
  );
}
