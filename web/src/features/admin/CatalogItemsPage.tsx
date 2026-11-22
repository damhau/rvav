import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Workflow } from "lucide-react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CategoriesPanel } from "@/features/admin/catalog/CategoriesPanel";
import { GitBanner } from "@/features/admin/catalog/GitPanels";
import { ItemsPanel } from "@/features/admin/catalog/ItemsPanel";
import { SecretsPanel } from "@/features/admin/catalog/SecretsPanel";
import { api, call, isApiError } from "@/lib/api/client";
import type { AwxTemplate } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { relative } from "@/lib/format";

export const catalogTabs = ["items", "categories", "secrets", "awx"] as const;
export type CatalogTab = (typeof catalogTabs)[number];

function Status({ t }: { t: AwxTemplate }) {
  if (t.gone) return <Badge tone="neutral">Not in AWX</Badge>;
  if (t.survey_changed) return <Badge tone="warn">Survey changed</Badge>;
  if (t.warnings.length > 0) return <Badge tone="warn">Check settings</Badge>;
  return <Badge tone="ok">In step</Badge>;
}

const awxColumns: Column<AwxTemplate>[] = [
  {
    id: "template",
    header: "AWX template",
    sortValue: (t) => t.name,
    cell: (t) => (
      <>
        <div className="font-medium">{t.name}</div>
        <div className="font-mono text-caption text-muted">
          {t.workflow ? "Workflow" : "Job template"} #{t.template_id}
        </div>
      </>
    ),
  },
  {
    id: "item",
    header: "Catalog item",
    sortValue: (t) => t.item_name,
    cell: (t) =>
      t.item_id ? (
        <Link
          to="/admin/catalog-items/$itemId"
          params={{ itemId: t.item_id }}
          className="font-medium text-fg hover:text-accent"
        >
          {t.item_name}
        </Link>
      ) : (
        <span className="text-muted">None</span>
      ),
  },
  { id: "status", header: "Status", cell: (t) => <Status t={t} /> },
  {
    id: "changed",
    header: "What changed",
    cell: (t) =>
      t.survey_diff.length + t.warnings.length > 0 ? (
        <ul
          aria-label={`Changes and warnings for ${t.name}`}
          className="flex flex-col gap-0.5 text-secondary text-muted"
        >
          {t.survey_diff.map((d) => (
            <li key={d} className="font-mono text-caption text-fg">
              {d}
            </li>
          ))}
          {t.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : (
        <span className="text-muted">Nothing</span>
      ),
  },
  {
    id: "synced",
    header: "Synced",
    align: "end",
    sortValue: (t) => t.synced_at,
    cell: (t) => <span className="text-secondary text-muted">{relative(t.synced_at)}</span>,
  },
];

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
  return (
    <Panel
      title="Imported from AWX"
      description="Templates of rvav's AWX organisation with the catalog label become items, every few minutes or when you sync."
      flush
      actions={
        <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? "Syncing…" : "Sync now"}
        </Button>
      }
    >
      <DataTable
        label="AWX templates"
        columns={awxColumns}
        rows={templates.data?.items}
        rowKey={(t) => `${t.workflow}-${t.template_id}`}
        minWidth={880}
        loading={templates.isPending}
        error={templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
        empty={
          <EmptyState
            icon={Workflow}
            title="No AWX templates yet"
            body="Give a job template or workflow the catalog label in AWX, then sync: it becomes an item here."
          />
        }
      />
    </Panel>
  );
}

export function CatalogItemsPage({ tab }: { tab?: CatalogTab }) {
  const navigate = useNavigate();
  return (
    <Page>
      <PageHeader
        title="Catalog items"
        description="Build items, publish their versions and choose which projects may request them."
      />
      <Tabs
        value={tab ?? "items"}
        onValueChange={(v) =>
          void navigate({ to: "/admin/catalog-items", search: { tab: v as CatalogTab }, replace: true })
        }
      >
        <TabsList aria-label="Catalog sections">
          <TabsTrigger value="items">Items</TabsTrigger>
          <TabsTrigger value="categories">Categories</TabsTrigger>
          <TabsTrigger value="secrets">Secrets</TabsTrigger>
          <TabsTrigger value="awx">AWX templates</TabsTrigger>
        </TabsList>
        <TabsContent value="items" className="flex flex-col gap-3 pt-4">
          <GitBanner />
          <ItemsPanel />
        </TabsContent>
        <TabsContent value="categories" className="pt-4">
          <CategoriesPanel />
        </TabsContent>
        <TabsContent value="secrets" className="pt-4">
          <SecretsPanel />
        </TabsContent>
        <TabsContent value="awx" className="pt-4">
          <AwxTemplatesPanel />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
