import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LayoutGrid, SearchX } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { TableSearch } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Select } from "@/components/ui/select";
import type { CatalogItem, CatalogItemType } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { roughDuration } from "@/lib/format";
import { approvalHint, catalogKinds, versionLabel } from "./facts";
import { FavouriteStar } from "./favourite";

export interface CatalogSearch {
  category?: string;
  q?: string;
  type?: CatalogItemType;
}

function Card({ item, mayRequest }: { item: CatalogItem; mayRequest: boolean }) {
  const { t } = useTranslation("catalog");
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
            {itemTypeLabel(item.type)}
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
            {approvalHint(item.approval)}
          </span>
        )}
        {mayRequest && (
          <Button asChild size="sm" className="relative z-10 ml-auto">
            <Link to="/catalog/$itemId/request" params={{ itemId: item.slug }}>
              <Trans
                t={t}
                i18nKey="page.requestItem"
                values={{ name: item.name }}
                components={{ sr: <span className="sr-only" /> }}
              />
            </Link>
          </Button>
        )}
      </div>
    </div>
  );
}

export function CatalogPage({ search }: { search: CatalogSearch }) {
  const { t } = useTranslation("catalog");
  const { projectId, project, me } = useCurrentProject();
  const mayRequest = can(me, "request.submit", projectId);
  const navigate = useNavigate({ from: "/catalog/" });
  // The address keeps the search, without a history entry per keystroke.
  const [text, setText] = useSearchBox(
    search.q,
    (next) => void navigate({ search: (s: CatalogSearch) => ({ ...s, q: next }), replace: true }),
  );
  const categories = useQuery(q.categories(projectId));
  const items = useQuery(q.catalog({ projectId, category: search.category, q: search.q, type: search.type }));
  const filtered = Boolean(search.q || search.category || search.type);

  const set = (patch: Partial<CatalogSearch>) =>
    void navigate({ search: (s: CatalogSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () => {
    void navigate({ search: {}, replace: true });
  };
  const count = items.data?.items.length;

  return (
    <Page>
      <PageHeader
        title={t("page.title")}
        description={t(mayRequest ? "page.leadMayRequest" : "page.leadMayNotRequest", { project: project.name })}
      />
      <div className="flex flex-wrap items-center gap-2">
        <TableSearch
          label={t("page.search")}
          placeholder={t("page.searchPlaceholder")}
          value={text}
          onChange={setText}
          className="w-72"
        />
        <Select
          aria-label={t("page.category")}
          value={search.category ?? "any"}
          onChange={(v) => set({ category: v === "any" ? undefined : v })}
          loading={categories.isPending}
          options={[
            { value: "any", label: t("page.allCategories") },
            ...(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name, meta: String(c.item_count) })),
          ]}
          className="w-48"
        />
        <Select
          aria-label={t("page.kind")}
          value={search.type ?? "any"}
          onChange={(v) => set({ type: v === "any" ? undefined : (v as CatalogItemType) })}
          options={[
            { value: "any", label: t("page.allKinds") },
            ...catalogKinds.map((k) => ({ value: k, label: itemTypeLabel(k) })),
          ]}
          className="w-48"
        />
        {filtered && (
          <Button variant="quiet" onClick={clear}>
            {t("page.clear")}
          </Button>
        )}
        <span className="ml-auto text-secondary text-muted" aria-live="polite">
          {count === undefined ? "" : t("page.count", { count })}
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
              title={t("page.noMatch")}
              body={t("page.noMatchBody")}
              action={<Button onClick={clear}>{t("page.clear")}</Button>}
            />
          ) : (
            <EmptyState
              icon={LayoutGrid}
              title={t("page.nothingShared", { project: project.name })}
              body={t("page.nothingSharedBody")}
            />
          )}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
          {items.data.items.map((i) => (
            <Card key={i.id} item={i} mayRequest={mayRequest} />
          ))}
        </div>
      )}
    </Page>
  );
}
