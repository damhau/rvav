import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LayoutGrid, SearchX } from "lucide-react";
import { useEffect, useState } from "react";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { TableSearch } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Select } from "@/components/ui/select";
import type { CatalogItem, CatalogItemType } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { roughDuration } from "@/lib/utils";
import { approvalHint, catalogKinds, versionLabel } from "./facts";
import { FavouriteStar } from "./favourite";

export interface CatalogSearch {
  category?: string;
  q?: string;
  type?: CatalogItemType;
}

function Card({ item }: { item: CatalogItem }) {
  const duration = roughDuration(item.typical_duration_seconds);
  return (
    <div className="relative flex flex-col gap-3 rounded-lg border border-line bg-surface p-4 transition-colors focus-within:border-line-strong hover:border-line-strong">
      <div className="flex items-start gap-3">
        <ItemIcon name={item.icon} type={item.type} className="size-9" />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Link
            to="/catalog/$itemId"
            params={{ itemId: item.slug }}
            className="font-semibold break-words text-fg after:absolute after:inset-0 after:rounded-lg after:content-[''] hover:underline hover:underline-offset-2"
          >
            {item.name}
          </Link>
          <span className="text-caption text-muted">
            {itemTypeLabel[item.type]}
            {item.source.awx_template_name ? ` · ${item.source.awx_template_name}` : ""}
          </span>
        </div>
        <FavouriteStar item={item} />
      </div>
      <p className="line-clamp-2 min-h-10 text-secondary text-muted">{item.summary}</p>
      <div className="mt-auto flex flex-wrap items-center gap-x-2 gap-y-1.5 border-t border-line pt-3 text-caption text-muted">
        <span className="font-mono">{versionLabel(item)}</span>
        {duration && (
          <>
            <span aria-hidden>·</span>
            <span>{duration}</span>
          </>
        )}
        {item.approval !== "never" && (
          <span className="rounded-sm bg-warn-soft px-1.5 py-px font-medium text-warn">
            {approvalHint[item.approval]}
          </span>
        )}
        <Button asChild size="sm" className="relative z-10 ml-auto">
          <Link to="/catalog/$itemId/request" params={{ itemId: item.slug }}>
            Request<span className="sr-only"> {item.name}</span>
          </Link>
        </Button>
      </div>
    </div>
  );
}

export function CatalogPage({ search }: { search: CatalogSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/catalog/" });
  const [text, setText] = useState(search.q ?? "");
  const categories = useQuery(q.categories(projectId));
  const items = useQuery(q.catalog({ projectId, category: search.category, q: search.q, type: search.type }));
  const filtered = Boolean(search.q || search.category || search.type);

  // Keep the URL in step with the search box, without a history entry per keystroke.
  useEffect(() => {
    const t = setTimeout(() => {
      const next = text.trim() || undefined;
      if (next !== search.q) void navigate({ search: (s: CatalogSearch) => ({ ...s, q: next }), replace: true });
    }, 250);
    return () => clearTimeout(t);
  }, [text, search.q, navigate]);

  const set = (patch: Partial<CatalogSearch>) =>
    void navigate({ search: (s: CatalogSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () => {
    setText("");
    void navigate({ search: {}, replace: true });
  };
  const count = items.data?.items.length;

  return (
    <Page>
      <PageHeader
        title="Catalog"
        description={`What you can request in ${project.name}. Open an item to see what it builds, then request it.`}
      />
      <div className="flex flex-wrap items-center gap-2">
        <TableSearch
          label="Search the catalog"
          placeholder="Search by name or summary"
          value={text}
          onChange={setText}
          className="w-72"
        />
        <Select
          aria-label="Category"
          value={search.category ?? "any"}
          onChange={(v) => set({ category: v === "any" ? undefined : v })}
          loading={categories.isPending}
          options={[
            { value: "any", label: "All categories" },
            ...(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name, meta: String(c.item_count) })),
          ]}
          className="w-48"
        />
        <Select
          aria-label="Kind"
          value={search.type ?? "any"}
          onChange={(v) => set({ type: v === "any" ? undefined : (v as CatalogItemType) })}
          options={[
            { value: "any", label: "All kinds" },
            ...catalogKinds.map((t) => ({ value: t, label: itemTypeLabel[t] })),
          ]}
          className="w-48"
        />
        {filtered && (
          <Button variant="quiet" onClick={clear}>
            Clear filters
          </Button>
        )}
        <span className="ml-auto text-secondary text-muted" aria-live="polite">
          {count === undefined ? "" : `${count} ${count === 1 ? "item" : "items"}`}
        </span>
      </div>
      {items.isPending ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <Skeleton key={i} className="h-40 rounded-lg" />
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
          {filtered ? (
            <EmptyState
              icon={SearchX}
              title="No items match"
              body="Try other words or another category. Ask a catalog administrator if something you need is missing."
              action={<Button onClick={clear}>Clear filters</Button>}
            />
          ) : (
            <EmptyState
              icon={LayoutGrid}
              title={`Nothing is shared with ${project.name} yet`}
              body="Catalog administrators share items with projects. Ask them for what you need, or switch to another project."
            />
          )}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
          {items.data.items.map((i) => (
            <Card key={i.id} item={i} />
          ))}
        </div>
      )}
    </Page>
  );
}
