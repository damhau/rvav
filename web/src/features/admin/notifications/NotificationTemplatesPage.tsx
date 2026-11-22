// Notification templates (ADR-0059): each kind's message per channel and
// language, the built-in one until an administrator edits it.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { NotificationTemplateKind } from "@/lib/api/types";
import { adm } from "@/lib/query";
import { relative } from "@/lib/format";
import { channelLabel, localeLabel } from "./templateLabels";
import { kindName, kindWhen } from "@/lib/i18n/names";

const edited = (k: NotificationTemplateKind) => k.templates.filter((t) => t.edited);

const columns = (t: TFunction<"admin-notifications">): Column<NotificationTemplateKind>[] => [
  {
    id: "kind",
    header: t("list.notification"),
    sortValue: (k) => kindName(k),
    cell: (k) => (
      <>
        <Link
          to="/admin/notification-templates/$kind"
          params={{ kind: k.kind }}
          className="font-medium text-fg hover:underline"
        >
          {kindName(k)}
        </Link>
        <div className="text-caption text-muted">{kindWhen(k)}</div>
      </>
    ),
  },
  {
    id: "edited",
    header: t("list.edited"),
    sortValue: (k) => edited(k).length,
    cell: (k) => {
      const list = edited(k);
      if (!list.length) return <span className="text-secondary text-muted">{t("list.builtIn")}</span>;
      return (
        <div className="flex flex-wrap gap-1">
          {list.map((e) => (
            <Badge key={`${e.channel}-${e.locale}`} tone="accent">
              {t("list.editedIn", { channel: channelLabel(e.channel), locale: localeLabel(e.locale) })}
            </Badge>
          ))}
        </div>
      );
    },
  },
  {
    id: "changed",
    header: t("list.changed"),
    sortValue: (k) =>
      edited(k)
        .map((t) => t.updated_at ?? "")
        .sort()
        .at(-1) ?? "",
    cell: (k) => {
      const last = edited(k).sort((a, b) => (b.updated_at ?? "").localeCompare(a.updated_at ?? ""))[0];
      return (
        <span className="text-secondary text-muted">
          {last?.updated_at
            ? last.updated_by
              ? t("list.changedBy", { when: relative(last.updated_at), who: last.updated_by })
              : relative(last.updated_at)
            : t("list.never")}
        </span>
      );
    },
  },
];

export function NotificationTemplatesPage() {
  const { t } = useTranslation(["admin-notifications", "shell"]);
  const catalog = useQuery(adm.notificationTemplates());
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.notificationTemplates")} description={t("list.lead")} />
      <Panel flush>
        <DataTable
          label={t("shell:nav.admin.notificationTemplates")}
          columns={columns(t)}
          rows={catalog.data?.kinds}
          rowKey={(k) => k.kind}
          rowLinks
          minWidth={720}
          loading={catalog.isPending}
          error={catalog.isError && <ErrorState error={catalog.error} onRetry={() => void catalog.refetch()} />}
        />
      </Panel>
    </Page>
  );
}
