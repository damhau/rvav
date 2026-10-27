import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { CategoriesPanel } from "@/features/admin/catalog/CategoriesPanel";
import { ItemsPanel } from "@/features/admin/catalog/ItemsPanel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, call, isApiError } from "@/lib/api/client";
import type { AwxTemplate } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { relative } from "@/lib/utils";

function Status({ t }: { t: AwxTemplate }) {
  if (t.gone) return <Badge tone="neutral">Not in AWX</Badge>;
  if (t.survey_changed) return <Badge tone="warn">Survey changed</Badge>;
  if (t.warnings.length > 0) return <Badge tone="warn">Check settings</Badge>;
  return <Badge tone="ok">In step</Badge>;
}

function Row({ t }: { t: AwxTemplate }) {
  const notes = [...t.survey_diff, ...t.warnings];
  return (
    <tr className="hover:bg-sunken">
      <td className="border-b border-line px-4 py-2 align-top">
        <div className="font-medium">{t.name}</div>
        <div className="font-mono text-[11px] text-faint">
          {t.workflow ? "Workflow" : "Job template"} #{t.template_id}
        </div>
      </td>
      <td className="border-b border-line px-3 py-2 align-top">
        {t.item_id ? (
          <Link
            to="/admin/catalog-items/$itemId"
            params={{ itemId: t.item_id }}
            className="font-medium text-fg hover:text-accent"
          >
            {t.item_name}
          </Link>
        ) : (
          "—"
        )}
      </td>
      <td className="border-b border-line px-3 py-2 align-top">
        <Status t={t} />
      </td>
      <td className="border-b border-line px-3 py-2 align-top text-xs text-muted">
        {notes.length > 0 ? (
          <ul aria-label={`Changes and warnings for ${t.name}`} className="flex flex-col gap-0.5">
            {t.survey_diff.map((d) => (
              <li key={d} className="font-mono text-[11px] text-fg">
                {d}
              </li>
            ))}
            {t.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        ) : (
          "—"
        )}
      </td>
      <td className="border-b border-line px-4 py-2 text-right align-top text-xs text-faint">
        {relative(t.synced_at)}
      </td>
    </tr>
  );
}

export function AwxTemplatesPanel() {
  const qc = useQueryClient();
  const templates = useQuery(q.awxTemplates());
  const sync = useMutation({
    mutationFn: () => call(api.POST("/admin/awx/templates/sync")),
    onSuccess: (list) => {
      qc.setQueryData(keys.awxTemplates, list);
      void qc.invalidateQueries({ queryKey: keys.catalog.all });
      toast.success("AWX templates synced");
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "The sync failed", {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  const items = templates.data?.items ?? [];
  return (
    <Panel
      title="Imported from AWX"
      flush
      actions={
        <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? "Syncing…" : "Sync now"}
        </Button>
      }
    >
      {templates.isPending && <SkeletonRows rows={4} cols={5} />}
      {templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
      {templates.data && items.length === 0 && (
        <EmptyState
          title="No AWX templates yet"
          body="Templates of rvav's AWX organisation with the catalog label become items here, every few minutes or when you sync."
        />
      )}
      {items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[880px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                  AWX template
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Catalog item
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Status
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  What changed
                </th>
                <th scope="col" className="border-b border-line px-4 py-2 text-right font-medium">
                  Synced
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((t) => (
                <Row key={`${t.workflow}-${t.template_id}`} t={t} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

export function CatalogItemsPage() {
  return (
    <Page>
      <PageHeader
        title="Catalog items"
        meta="Build items, publish their versions and choose which projects may request them."
      />
      <ItemsPanel />
      <CategoriesPanel />
      <AwxTemplatesPanel />
    </Page>
  );
}
