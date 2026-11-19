// Notification templates (ADR-0059): each kind's message per channel and
// language, the built-in one until an administrator edits it.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { NotificationTemplateKind } from "@/lib/api/types";
import { adm } from "@/lib/query";
import { relative } from "@/lib/utils";
import { channelLabel, localeLabel } from "./templateLabels";

const edited = (k: NotificationTemplateKind) => k.templates.filter((t) => t.edited);

const columns: Column<NotificationTemplateKind>[] = [
  {
    id: "kind",
    header: "Notification",
    sortValue: (k) => k.name,
    cell: (k) => (
      <>
        <Link
          to="/admin/notification-templates/$kind"
          params={{ kind: k.kind }}
          className="font-medium text-fg hover:underline"
        >
          {k.name}
        </Link>
        <div className="text-caption text-muted">{k.when}</div>
      </>
    ),
  },
  {
    id: "edited",
    header: "Edited",
    sortValue: (k) => edited(k).length,
    cell: (k) => {
      const list = edited(k);
      if (!list.length) return <span className="text-secondary text-muted">Built-in templates</span>;
      return (
        <div className="flex flex-wrap gap-1">
          {list.map((t) => (
            <Badge key={`${t.channel}-${t.locale}`} tone="accent">
              {channelLabel[t.channel]}, {localeLabel[t.locale]}
            </Badge>
          ))}
        </div>
      );
    },
  },
  {
    id: "changed",
    header: "Changed",
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
            ? `${relative(last.updated_at)}${last.updated_by ? ` by ${last.updated_by}` : ""}`
            : "Never"}
        </span>
      );
    },
  },
];

export function NotificationTemplatesPage() {
  const catalog = useQuery(adm.notificationTemplates());
  return (
    <Page>
      <PageHeader
        title="Notification templates"
        description="What each notification says, by email, in Teams and in Slack, in English and in French. An edited template replaces the built-in one for the notifications sent from then on; the greeting, the facts and the button stay rvav's."
      />
      <Panel flush>
        <DataTable
          label="Notification templates"
          columns={columns}
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
