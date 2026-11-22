// The System page (U1.5, ADR-0030), read only: the version and schema,
// rvav's processes and the settings revision each runs, the settings saved
// in Administration and how far each has applied, and the install
// settings, which are changed in the environment only.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { CircleCheck, Clock, LoaderCircle, Lock, Server, SlidersHorizontal } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { KeyValues, Page } from "@/components/common/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { cn } from "@/lib/utils";
import { fullDate, relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { roleName } from "./Applying";
import { integrationPage, integrationTitle } from "./IntegrationSettings";

type S = components["schemas"];

// "latest": built without GOFIPS140, with its Go release's module.
const fipsModule = (module: string) =>
  module === "latest" ? i18n.t("admin:system.fips.latest") : i18n.t("admin:system.fips.module", { module });

function Fips({ status }: { status: S["FipsStatus"] }) {
  const { t } = useTranslation("admin");
  return (
    <>
      <Trans
        t={t}
        i18nKey="system.fips.line"
        values={{ mode: t(`system.fips.mode.${status.mode}`), means: t(`system.fips.means.${status.mode}`) }}
        components={{ means: <span className="text-muted" /> }}
      />
      <span className="ml-1.5 font-mono text-secondary text-faint">{fipsModule(status.module)}</span>
    </>
  );
}

function Applies({ value }: { value: S["SettingsRevision"]["applies"] }) {
  const { t } = useTranslation("admin");
  const Icon = value === "applying" ? LoaderCircle : value === "applied" ? CircleCheck : Clock;
  return (
    <span className="inline-flex items-center gap-1.5">
      <Icon
        className={cn(
          "size-3.5",
          value === "applying" ? "animate-spin text-warn" : value === "applied" ? "text-ok" : "text-muted",
        )}
        aria-hidden
      />
      {t(`system.applies.${value}`)}
    </span>
  );
}

function When({ iso }: { iso: string }) {
  return (
    <span className="tnum whitespace-nowrap" title={fullDate(iso)}>
      {relative(iso)}
    </span>
  );
}

// No column sorts: the page has nothing to press, and each list is short.
function Processes({ system }: { system: S["System"] }) {
  const { t } = useTranslation("admin");
  const versions = new Set(system.processes.map((p) => p.version));
  const modes = new Set(system.processes.map((p) => p.fips.mode));
  const columns: Column<S["Process"]>[] = [
    {
      id: "process",
      header: t("system.processes.process"),
      cell: (p) => (
        <>
          <span className="font-medium">{roleName(p.role)}</span>{" "}
          <span className="font-mono text-secondary text-faint">{p.host}</span>
        </>
      ),
    },
    {
      id: "version",
      header: t("system.processes.version"),
      cell: (p) => <span className="font-mono text-secondary">{p.version}</span>,
    },
    {
      id: "revision",
      header: t("system.processes.revision"),
      cell: (p) => (
        <>
          <span className="tnum">{p.revision}</span>
          {p.revision < system.revision && (
            <span className="ml-1.5 text-secondary text-warn">{t("system.processes.earlier")}</span>
          )}
        </>
      ),
    },
    {
      id: "fips",
      header: "FIPS 140-3",
      cell: (p) => <span title={fipsModule(p.fips.module)}>{t(`system.fips.mode.${p.fips.mode}`)}</span>,
    },
    { id: "state", header: t("system.processes.state"), cell: (p) => t(`system.state.${p.state}`) },
    { id: "started", header: t("system.processes.started"), cell: (p) => <When iso={p.started_at} /> },
  ];
  return (
    <Panel title={t("system.processes.title")} description={t("system.processes.description")} flush>
      <DataTable
        label={t("system.processes.title")}
        columns={columns}
        rows={system.processes}
        rowKey={(p) => p.id}
        minWidth={720}
        toolbar={
          (versions.size > 1 || modes.size > 1) && (
            <div role="status" className="space-y-1 text-secondary text-warn">
              {versions.size > 1 && <p>{t("system.processes.versions", { n: versions.size })}</p>}
              {modes.size > 1 && (
                <p>
                  <Trans
                    t={t}
                    i18nKey="system.processes.modes"
                    values={{ n: modes.size }}
                    components={{ env: <span className="font-mono" /> }}
                  />
                </p>
              )}
            </div>
          )
        }
        empty={
          <EmptyState icon={Server} title={t("system.processes.noneTitle")} body={t("system.processes.noneBody")} />
        }
      />
    </Panel>
  );
}

function Revisions({ system }: { system: S["System"] }) {
  const { t } = useTranslation("admin");
  const columns: Column<S["SettingsRevision"]>[] = [
    {
      id: "integration",
      header: t("system.revisions.integration"),
      cell: (r) => (
        <Link
          to="/admin/integrations/$kind"
          params={{ kind: integrationPage(r.kind) }}
          className="font-medium text-accent underline-offset-2 hover:underline"
        >
          {integrationTitle(r.kind)}
        </Link>
      ),
    },
    { id: "revision", header: t("system.revisions.revision"), cell: (r) => <span className="tnum">{r.revision}</span> },
    {
      id: "saved",
      header: t("system.revisions.saved"),
      cell: (r) => (
        <>
          <When iso={r.updated_at} />
          {r.updated_by && <span className="text-muted">{t("system.revisions.by", { name: r.updated_by.name })}</span>}
        </>
      ),
    },
    { id: "applies", header: t("system.revisions.applies"), cell: (r) => <Applies value={r.applies} /> },
  ];
  return (
    <Panel title={t("system.revisions.title")} description={t("system.revisions.description")} flush>
      <DataTable
        label={t("system.revisions.title")}
        columns={columns}
        rows={system.settings}
        rowKey={(r) => r.kind}
        minWidth={560}
        empty={
          <EmptyState
            icon={SlidersHorizontal}
            title={t("system.revisions.noneTitle")}
            body={t("system.revisions.noneBody")}
          />
        }
      />
    </Panel>
  );
}

const installColumns = (t: TFunction<"admin">): Column<S["System"]["install"][number]>[] => [
  {
    id: "setting",
    header: t("system.install.setting"),
    cell: (s) => <span className="font-mono text-secondary">{s.name}</span>,
  },
  {
    id: "value",
    header: t("system.install.value"),
    className: "break-all",
    cell: (s) =>
      s.value !== undefined && s.value !== "" ? (
        <span className="font-mono text-secondary">
          {s.value}
          {s.secret && (
            <span className="ml-1.5 font-sans text-faint">
              <Lock className="mr-0.5 inline size-3" aria-hidden />
              {t("system.install.passwordHidden")}
            </span>
          )}
        </span>
      ) : s.secret ? (
        <span className="inline-flex items-center gap-1">
          <Lock className="size-3 text-faint" aria-hidden />
          {s.set ? t("system.install.set") : t("system.install.notSet")}
        </span>
      ) : (
        <span className="text-faint">{t("system.install.none")}</span>
      ),
  },
  {
    id: "from",
    header: t("system.install.from"),
    cell: (s) => (
      <span className={s.set ? "text-secondary" : "text-secondary text-muted"}>
        {s.set ? t("system.install.environment") : t("system.install.default")}
      </span>
    ),
  },
];

function Install({ system }: { system: S["System"] }) {
  const { t } = useTranslation("admin");
  return (
    <Panel title={t("system.install.title")} description={t("system.install.description")} flush>
      <DataTable
        label={t("system.install.title")}
        columns={installColumns(t)}
        rows={system.install}
        rowKey={(s) => s.name}
        minWidth={560}
      />
    </Panel>
  );
}

export function SystemPage() {
  const { t } = useTranslation(["admin", "shell"]);
  const system = useQuery({ ...adm.system(), refetchInterval: 10_000 });
  const d = system.data;
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.system")}
        description={
          <Trans
            t={t}
            i18nKey="system.description"
            components={{
              integrations: <Link to="/admin/integrations" className="text-accent underline underline-offset-2" />,
            }}
          />
        }
      />
      {system.isPending && <SkeletonRows rows={6} cols={3} />}
      {system.isError && <ErrorState error={system.error} onRetry={() => void system.refetch()} />}
      {d && (
        <>
          <Panel title={t("system.thisInstall")}>
            <KeyValues
              items={[
                { label: t("system.version"), value: <span className="font-mono text-secondary">{d.version}</span> },
                { label: t("system.fipsMode"), value: <Fips status={d.fips} /> },
                { label: t("system.schema"), value: t("system.migration", { n: d.schema }) },
                { label: t("system.connections"), value: t("system.revision", { n: d.revision }) },
              ]}
            />
          </Panel>
          <Processes system={d} />
          <Revisions system={d} />
          <Install system={d} />
        </>
      )}
    </Page>
  );
}
