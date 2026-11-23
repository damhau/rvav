// The audit log (ADR-0020): who did what, when and in which project,
// searched and exported as CSV. Administrators and auditors see all of it,
// project admins their projects' events.
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { Link, useSearch } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { Download, ScrollText } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { KeyValues, Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { adminPageAt } from "@/components/shell/nav";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterEmpty, FilterInput, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { Me } from "@/lib/api/types";
import { can, projectsWith } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm, q, type AuditFilters } from "@/lib/query";
import { fullDate } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type AuditEvent = components["schemas"]["AuditEvent"];

/** The audit log's filters, in the address. `from` and `to` are local days, `to` included. */
export interface AuditSearch {
  q?: string;
  kind?: string;
  project?: string;
  from?: string;
  to?: string;
}

const kinds = [
  { value: "catalog.", key: "catalog" },
  { value: "project.", key: "project" },
  { value: "member.", key: "member" },
  { value: "group_mapping.", key: "groupMapping" },
  { value: "environment.", key: "environment" },
  { value: "placement.", key: "placement" },
  { value: "network.", key: "network" },
  { value: "template.", key: "template" },
  { value: "size.", key: "size" },
  { value: "policy.", key: "policy" },
  { value: "naming.", key: "naming" },
  { value: "settings.", key: "settings" },
  { value: "token.", key: "token" },
  { value: "auth.", key: "auth" },
  { value: "auth.sign_in_failed", key: "authFailed" },
] as const;

/** The start of a local day, or of the day after it: "To" includes the day chosen. */
function dayStart(day: string, after = false): string {
  const d = new Date(`${day}T00:00`);
  if (after) d.setDate(d.getDate() + 1);
  return d.toISOString();
}

const targetTypes = [
  "ad_placement",
  "api_token",
  "approval",
  "approval_policy",
  "audit_destination",
  "awx_action",
  "branding",
  "catalog_git_file",
  "catalog_item",
  "deployment",
  "deployment_limit_policy",
  "environment",
  "form_library",
  "group_mapping",
  "integration",
  "ip_range",
  "lease_policy",
  "lease_rule",
  "naming_policy",
  "network",
  "notification_template",
  "nsx_manager",
  "onboarding_plan",
  "placement_rule",
  "process",
  "project",
  "property_group",
  "quota",
  "request",
  "resource",
  "resource_type",
  "role",
  "role_assignment",
  "schedule",
  "servicenow_request",
  "setup",
  "size_preset",
  "subscription",
  "user",
  "variable",
  "vcenter",
  "vm_template",
  "vra_project",
  "vra_snapshot",
  "workflow",
] as const;
type TargetType = (typeof targetTypes)[number];
const isTargetType = (type: string): type is TargetType => (targetTypes as readonly string[]).includes(type);

/** What an event was on, in the language shown; a type newer than the page as the server names it. */
export const targetTypeLabel = (type: string) =>
  isTargetType(type) ? i18n.t(`admin:audit.target.${type}`) : type.replaceAll("_", " ");

const linkClass = "text-fg hover:text-accent";

// Both see every deployment of the project and every request in it, whatever
// its sharing (ADR-0045); any other reader may be refused one.
const seesProjectWork = (me: Me, projectId?: string) =>
  can(me, "project.see", projectId) && can(me, "deployments.read_all", projectId);

/** The target's name, a link to its page when rvav keeps it and the viewer may open that page. */
function TargetName({ e, name }: { e: AuditEvent; name: string }) {
  const { me } = useCurrentProject();
  const opens = (to: string) => adminPageAt(to)?.show(me) ?? false;
  const id = e.target_id;
  if (e.target_exists !== true) return <>{name}</>;
  switch (e.target_type) {
    case "deployment":
      if (!seesProjectWork(me, e.project?.id)) break;
      return (
        <Link to="/deployments/$deploymentId" params={{ deploymentId: id }} className={linkClass}>
          {name}
        </Link>
      );
    case "request":
      if (!seesProjectWork(me, e.project?.id)) break;
      return (
        <Link to="/requests/$requestId" params={{ requestId: id }} className={linkClass}>
          {name}
        </Link>
      );
    case "project":
      if (!opens("/admin/projects")) break;
      return (
        <Link to="/admin/projects/$projectId" params={{ projectId: id }} className={linkClass}>
          {name}
        </Link>
      );
    case "catalog_item":
      if (!opens("/admin/catalog-items")) break;
      return (
        <Link to="/admin/catalog-items/$itemId" params={{ itemId: id }} className={linkClass}>
          {name}
        </Link>
      );
    case "role":
      if (!opens("/admin/roles")) break;
      return (
        <Link to="/admin/roles/$roleKey" params={{ roleKey: id }} className={linkClass}>
          {name}
        </Link>
      );
    case "subscription":
      if (!opens("/admin/subscriptions")) break;
      return (
        <Link to="/admin/subscriptions/$subscriptionId" params={{ subscriptionId: id }} className={linkClass}>
          {name}
        </Link>
      );
  }
  return <>{name}</>;
}

/** What an event was on: its type, and its name when rvav has one, else its ID. */
function Target({ e }: { e: AuditEvent }) {
  const { t } = useTranslation("admin");
  return (
    <>
      <span className="text-faint">{targetTypeLabel(e.target_type)}</span>{" "}
      {e.target_name ? <TargetName e={e} name={e.target_name} /> : <span className="font-mono">{e.target_id}</span>}
      {e.target_exists === false && <span className="text-faint"> {t("audit.gone")}</span>}
    </>
  );
}

const detailCount = (e: AuditEvent) => Object.keys(e.detail).length;

/** Who did it: rvav itself when nobody did, but for a sign-in nobody was known for. */
const who = (e: AuditEvent) => e.actor?.name ?? (e.action.startsWith("auth.") ? i18n.t("admin:audit.unknown") : "rvav");

/** The address a sign-in or a token's use came from (ADR-0058). */
const from = (e: AuditEvent) => (typeof e.detail.ip === "string" ? e.detail.ip : undefined);

const value = (v: unknown) =>
  typeof v === "string" ? (
    v
  ) : (
    <pre className="overflow-auto rounded bg-sunken p-2 font-mono text-caption whitespace-pre-wrap">
      {JSON.stringify(v, null, 2)}
    </pre>
  );

function EventSheet({ e, onClose }: { e: AuditEvent; onClose: () => void }) {
  const { t } = useTranslation("admin");
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()} title={e.action} description={fullDate(e.at)}>
      <KeyValues
        items={[
          {
            label: t("audit.who"),
            value: !e.actor ? who(e) : e.actor.email ? `${e.actor.name} (${e.actor.email})` : e.actor.name,
          },
          { label: t("audit.what"), value: <span className="font-mono">{e.action}</span> },
          {
            label: t("audit.on"),
            value: (
              <>
                <Target e={e} />
                {e.target_name && e.target_id && <span className="block font-mono text-muted">{e.target_id}</span>}
              </>
            ),
          },
          { label: t("audit.project"), value: e.project?.name ?? t("audit.none") },
          { label: t("audit.event"), value: <span className="font-mono">{e.id}</span> },
        ]}
      />
      <section aria-labelledby="audit-detail" className="flex flex-col gap-2">
        <h3 id="audit-detail" className="label">
          {t("audit.detail")}
        </h3>
        {detailCount(e) === 0 ? (
          <p className="text-secondary text-muted">{t("audit.nothingMore")}</p>
        ) : (
          <KeyValues items={Object.entries(e.detail).map(([k, v]) => ({ label: k, value: value(v) }))} />
        )}
      </section>
    </Sheet>
  );
}

const columns = (t: TFunction<"admin">, open: (e: AuditEvent) => void): Column<AuditEvent>[] => [
  {
    id: "when",
    header: t("audit.when"),
    cell: (e) => (
      <button
        type="button"
        className="text-left text-secondary whitespace-nowrap text-muted hover:text-accent"
        onClick={() => open(e)}
      >
        <span className="sr-only">{t("audit.openFrom")}</span>
        <time dateTime={e.at}>{fullDate(e.at)}</time>
      </button>
    ),
  },
  {
    id: "who",
    header: t("audit.who"),
    cell: (e) => (
      <span className="text-secondary">
        {who(e)}
        {from(e) && <span className="block font-mono text-muted">{t("audit.from", { ip: from(e) })}</span>}
      </span>
    ),
  },
  { id: "what", header: t("audit.what"), cell: (e) => <span className="font-mono text-secondary">{e.action}</span> },
  {
    id: "on",
    header: t("audit.on"),
    cell: (e) => (
      <span className="text-secondary">
        <Target e={e} />
      </span>
    ),
  },
  {
    id: "project",
    header: t("audit.project"),
    cell: (e) => (
      <span className="text-secondary">{e.project?.name ?? <span className="text-faint">{t("audit.none")}</span>}</span>
    ),
  },
  {
    id: "detail",
    header: t("audit.detail"),
    cell: (e) => (
      <span className="text-secondary text-muted">
        {detailCount(e) === 0 ? t("audit.none") : t("audit.details", { count: detailCount(e) })}
      </span>
    ),
  },
];

export function AuditPage() {
  const { t } = useTranslation(["admin", "shell", "common"]);
  const { me } = useCurrentProject();
  // Those who read the audit log of every project filter by any, archived
  // ones too; project admins by theirs.
  const scope = projectsWith(me, "audit.read");
  const allProjects = useQuery({ ...adm.projectNames(true), enabled: scope === "all" });
  const projectOptions =
    scope === "all"
      ? (allProjects.data?.items ?? []).map((p) => ({
          value: p.id,
          label: p.archived ? t("audit.archived", { name: p.name }) : p.name,
        }))
      : scope.map((p) => ({ value: p.id, label: p.name }));
  const search: AuditSearch = useSearch({ from: "/admin/audit" });
  const bar = useAddressFilters(search, ["q", "kind", "project", "from", "to"]);
  const [opened, setOpened] = useState<AuditEvent | null>(null);
  const filters: AuditFilters = {
    q: search.q,
    action: search.kind,
    project_id: search.project,
    from: search.from ? dayStart(search.from) : undefined,
    to: search.to ? dayStart(search.to, true) : undefined,
  };
  const events = useInfiniteQuery(q.audit(filters));
  const list = events.data?.pages.flatMap((p) => p.items);
  const exportCsv = useMutation({
    mutationFn: async () => {
      const text = await call(api.GET("/audit", { params: { query: { ...filters, format: "csv" } }, parseAs: "text" }));
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/csv" }));
      Object.assign(document.createElement("a"), { href: url, download: "rvav-audit.csv" }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : t("audit.exportFailed")),
  });
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.audit")}
        description={t("audit.description")}
        actions={
          <Button disabled={exportCsv.isPending} onClick={() => exportCsv.mutate()}>
            <Download />
            {exportCsv.isPending ? t("audit.exporting") : t("audit.export")}
          </Button>
        }
      />
      <Panel
        flush
        footer={
          events.hasNextPage && (
            <Button size="sm" disabled={events.isFetchingNextPage} onClick={() => void events.fetchNextPage()}>
              {events.isFetchingNextPage ? t("common:loadingEllipsis") : t("audit.loadOlder")}
            </Button>
          )
        }
      >
        <DataTable
          label={t("audit.events")}
          columns={columns(t, setOpened)}
          rows={list}
          rowKey={(e) => e.id}
          minWidth={820}
          loading={events.isPending}
          error={events.isError && <ErrorState error={events.error} onRetry={() => void events.refetch()} />}
          toolbar={
            <FilterBar
              filters={bar}
              search={{ label: t("audit.search"), placeholder: t("audit.searchPlaceholder"), className: "w-56" }}
              end={list ? t(events.hasNextPage ? "audit.shown" : "audit.count", { count: list.length }) : ""}
            >
              <FilterSelect
                name="kind"
                label={t("audit.what")}
                any={t("audit.kind.any")}
                options={kinds.map((k) => ({ value: k.value, label: t(`audit.kind.${k.key}`) }))}
                className="w-44"
              />
              <FilterSelect
                name="project"
                label={t("audit.project")}
                any={t("audit.allProjects")}
                loading={scope === "all" && allProjects.isPending}
                options={projectOptions}
                className="w-48"
              />
              <FilterInput name="from" type="date" label={t("audit.fromDay")} />
              <FilterInput name="to" type="date" label={t("audit.toDay")} />
            </FilterBar>
          }
          empty={
            bar.filtered ? (
              <FilterEmpty filters={bar} icon={ScrollText} title={t("audit.nothingFound")} body={t("audit.noMatch")} />
            ) : (
              <EmptyState icon={ScrollText} title={t("audit.nothingYet")} body={t("audit.nothingYetBody")} />
            )
          }
        />
      </Panel>
      {opened && <EventSheet e={opened} onClose={() => setOpened(null)} />}
    </Page>
  );
}
