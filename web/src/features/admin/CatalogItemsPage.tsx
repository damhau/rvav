import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { Workflow } from "lucide-react";
import { useTranslation } from "react-i18next";
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

function Status({ template }: { template: AwxTemplate }) {
  const { t } = useTranslation("admin");
  if (template.gone) return <Badge tone="neutral">{t("catalogItems.notInAwx")}</Badge>;
  if (template.survey_changed) return <Badge tone="warn">{t("catalogItems.surveyChanged")}</Badge>;
  if (template.warnings.length > 0) return <Badge tone="warn">{t("catalogItems.checkSettings")}</Badge>;
  return <Badge tone="ok">{t("catalogItems.inStep")}</Badge>;
}

const awxColumns = (t: TFunction<"admin">): Column<AwxTemplate>[] => [
  {
    id: "template",
    header: t("catalogItems.template"),
    sortValue: (x) => x.name,
    cell: (x) => (
      <>
        <div className="font-medium">{x.name}</div>
        <div className="font-mono text-caption text-muted">
          {x.workflow
            ? t("catalogItems.workflow", { id: x.template_id })
            : t("catalogItems.jobTemplate", { id: x.template_id })}
        </div>
      </>
    ),
  },
  {
    id: "item",
    header: t("catalogItems.item"),
    sortValue: (x) => x.item_name,
    cell: (x) =>
      x.item_id ? (
        <Link
          to="/admin/catalog-items/$itemId"
          params={{ itemId: x.item_id }}
          className="font-medium text-fg hover:text-accent"
        >
          {x.item_name}
        </Link>
      ) : (
        <span className="text-muted">{t("catalogItems.none")}</span>
      ),
  },
  { id: "status", header: t("catalogItems.status"), cell: (x) => <Status template={x} /> },
  {
    id: "changed",
    header: t("catalogItems.changed"),
    cell: (x) =>
      x.survey_diff.length + x.warnings.length > 0 ? (
        <ul
          aria-label={t("catalogItems.changesFor", { name: x.name })}
          className="flex flex-col gap-0.5 text-secondary text-muted"
        >
          {x.survey_diff.map((d) => (
            <li key={d} className="font-mono text-caption text-fg">
              {d}
            </li>
          ))}
          {x.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : (
        <span className="text-muted">{t("catalogItems.nothing")}</span>
      ),
  },
  {
    id: "synced",
    header: t("catalogItems.synced"),
    align: "end",
    sortValue: (x) => x.synced_at,
    cell: (x) => <span className="text-secondary text-muted">{relative(x.synced_at)}</span>,
  },
];

export function AwxTemplatesPanel() {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const templates = useQuery(q.awxTemplates());
  const sync = useMutation({
    mutationFn: () => call(api.POST("/admin/awx/templates/sync")),
    onSuccess: (list) => {
      qc.setQueryData(keys.awxTemplates, list);
      void qc.invalidateQueries({ queryKey: keys.catalog.all });
      toast.success(t("catalogItems.syncedToast"));
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : t("catalogItems.syncFailed"), {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  return (
    <Panel
      title={t("catalogItems.imported")}
      description={t("catalogItems.importedDescription")}
      flush
      actions={
        <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? t("catalogItems.syncing") : t("catalogItems.sync")}
        </Button>
      }
    >
      <DataTable
        label={t("catalogItems.awxTemplates")}
        columns={awxColumns(t)}
        rows={templates.data?.items}
        rowKey={(x) => `${x.workflow}-${x.template_id}`}
        minWidth={880}
        loading={templates.isPending}
        error={templates.isError && <ErrorState error={templates.error} onRetry={() => void templates.refetch()} />}
        empty={<EmptyState icon={Workflow} title={t("catalogItems.noneYet")} body={t("catalogItems.noneYetBody")} />}
      />
    </Panel>
  );
}

export function CatalogItemsPage({ tab }: { tab?: CatalogTab }) {
  const { t } = useTranslation(["admin", "shell"]);
  const navigate = useNavigate();
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.catalogItems")} description={t("catalogItems.description")} />
      <Tabs
        value={tab ?? "items"}
        onValueChange={(v) =>
          void navigate({ to: "/admin/catalog-items", search: { tab: v as CatalogTab }, replace: true })
        }
      >
        <TabsList aria-label={t("catalogItems.sections")}>
          <TabsTrigger value="items">{t("catalogItems.items")}</TabsTrigger>
          <TabsTrigger value="categories">{t("catalogItems.categories")}</TabsTrigger>
          <TabsTrigger value="secrets">{t("catalogItems.secrets")}</TabsTrigger>
          <TabsTrigger value="awx">{t("catalogItems.awxTemplates")}</TabsTrigger>
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
