// The System page (U1.5, ADR-0030), read only: the version and schema,
// rvav's processes and the settings revision each runs, the settings saved
// in Administration and how far each has applied, and the install
// settings, which are changed in the environment only.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, Clock, LoaderCircle, Lock, Server, SlidersHorizontal } from "lucide-react";
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
import { roles } from "./Applying";
import { integrationPage, integrationTitle } from "./IntegrationSettings";

type S = components["schemas"];

const applies: Record<S["SettingsRevision"]["applies"], string> = {
  next_use: "On next use",
  applying: "Applying",
  applied: "Applied",
};

const states: Record<S["Process"]["state"], string> = {
  running: "Running",
  draining: "Finishing its running work",
  restarting: "Starting again",
};

const fipsModes: Record<S["FipsStatus"]["mode"], string> = {
  off: "Off",
  on: "On",
  only: "Only",
};

const fipsMeans: Record<S["FipsStatus"]["mode"], string> = {
  off: "no FIPS 140 mode",
  on: "approved algorithms where rvav and Go choose",
  only: "other algorithms refused",
};

// "latest": built without GOFIPS140, with its Go release's module.
const fipsModule = (module: string) =>
  module === "latest" ? "Go's module, not a validated one" : `Go Cryptographic Module ${module}`;

function Fips({ status }: { status: S["FipsStatus"] }) {
  return (
    <>
      {fipsModes[status.mode]}
      <span className="text-muted">: {fipsMeans[status.mode]}</span>
      <span className="ml-1.5 font-mono text-secondary text-faint">{fipsModule(status.module)}</span>
    </>
  );
}

function Applies({ value }: { value: S["SettingsRevision"]["applies"] }) {
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
      {applies[value]}
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
  const versions = new Set(system.processes.map((p) => p.version));
  const modes = new Set(system.processes.map((p) => p.fips.mode));
  const columns: Column<S["Process"]>[] = [
    {
      id: "process",
      header: "Process",
      cell: (p) => (
        <>
          <span className="font-medium">{roles[p.role]}</span>{" "}
          <span className="font-mono text-secondary text-faint">{p.host}</span>
        </>
      ),
    },
    { id: "version", header: "Version", cell: (p) => <span className="font-mono text-secondary">{p.version}</span> },
    {
      id: "revision",
      header: "Settings revision",
      cell: (p) => (
        <>
          <span className="tnum">{p.revision}</span>
          {p.revision < system.revision && <span className="ml-1.5 text-secondary text-warn">earlier settings</span>}
        </>
      ),
    },
    {
      id: "fips",
      header: "FIPS 140-3",
      cell: (p) => <span title={fipsModule(p.fips.module)}>{fipsModes[p.fips.mode]}</span>,
    },
    { id: "state", header: "State", cell: (p) => states[p.state] },
    { id: "started", header: "Started", cell: (p) => <When iso={p.started_at} /> },
  ];
  return (
    <Panel title="Processes" description="Each of rvav's processes, and the settings revision it runs." flush>
      <DataTable
        label="Processes"
        columns={columns}
        rows={system.processes}
        rowKey={(p) => p.id}
        minWidth={720}
        toolbar={
          (versions.size > 1 || modes.size > 1) && (
            <div role="status" className="space-y-1 text-secondary text-warn">
              {versions.size > 1 && (
                <p>Processes run {versions.size} versions: an upgrade is under way, or a process was left behind.</p>
              )}
              {modes.size > 1 && (
                <p>
                  Processes run in {modes.size} FIPS 140-3 modes: give each the same{" "}
                  <span className="font-mono">RVAV_FIPS</span>, then start them again.
                </p>
              )}
            </div>
          )
        }
        empty={
          <EmptyState
            icon={Server}
            title="No process"
            body="No process has recorded itself in the last 30 seconds. Check that rvav's processes run."
          />
        }
      />
    </Panel>
  );
}

function Revisions({ system }: { system: S["System"] }) {
  const columns: Column<S["SettingsRevision"]>[] = [
    {
      id: "integration",
      header: "Integration",
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
    { id: "revision", header: "Revision", cell: (r) => <span className="tnum">{r.revision}</span> },
    {
      id: "saved",
      header: "Saved",
      cell: (r) => (
        <>
          <When iso={r.updated_at} />
          {r.updated_by && <span className="text-muted"> by {r.updated_by.name}</span>}
        </>
      ),
    },
    { id: "applies", header: "Applies", cell: (r) => <Applies value={r.applies} /> },
  ];
  return (
    <Panel title="Settings saved in Administration" description="The latest first, and how far each has applied." flush>
      <DataTable
        label="Settings saved in Administration"
        columns={columns}
        rows={system.settings}
        rowKey={(r) => r.kind}
        minWidth={560}
        empty={
          <EmptyState
            icon={SlidersHorizontal}
            title="Nothing saved yet"
            body="None yet: every setting comes from the environment or its default."
          />
        }
      />
    </Panel>
  );
}

const installColumns: Column<S["System"]["install"][number]>[] = [
  { id: "setting", header: "Setting", cell: (s) => <span className="font-mono text-secondary">{s.name}</span> },
  {
    id: "value",
    header: "Value",
    className: "break-all",
    cell: (s) =>
      s.value !== undefined && s.value !== "" ? (
        <span className="font-mono text-secondary">
          {s.value}
          {s.secret && (
            <span className="ml-1.5 font-sans text-faint">
              <Lock className="mr-0.5 inline size-3" aria-hidden />
              password hidden
            </span>
          )}
        </span>
      ) : s.secret ? (
        <span className="inline-flex items-center gap-1">
          <Lock className="size-3 text-faint" aria-hidden />
          {s.set ? "Set" : "Not set"}
        </span>
      ) : (
        <span className="text-faint">none</span>
      ),
  },
  {
    id: "from",
    header: "From",
    cell: (s) => (
      <span className={s.set ? "text-secondary" : "text-secondary text-muted"}>
        {s.set ? "Environment" : "Default"}
      </span>
    ),
  },
];

function Install({ system }: { system: S["System"] }) {
  return (
    <Panel
      title="Install settings"
      description={
        <>
          Set in the environment of each process, as the API answering reads them: change them there, then restart
          rvav&rsquo;s processes. A secret&rsquo;s value is never shown.
        </>
      }
      flush
    >
      <DataTable
        label="Install settings"
        columns={installColumns}
        rows={system.install}
        rowKey={(s) => s.name}
        minWidth={560}
      />
    </Panel>
  );
}

export function SystemPage() {
  const system = useQuery({ ...adm.system(), refetchInterval: 10_000 });
  const d = system.data;
  return (
    <Page>
      <PageHeader
        title="System"
        description={
          <>
            rvav as it runs now, read only. Settings saved in{" "}
            <Link to="/admin/integrations" className="text-accent underline underline-offset-2">
              Integrations
            </Link>{" "}
            apply as they say; install settings are changed in the environment.
          </>
        }
      />
      {system.isPending && <SkeletonRows rows={6} cols={3} />}
      {system.isError && <ErrorState error={system.error} onRetry={() => void system.refetch()} />}
      {d && (
        <>
          <Panel title="This install">
            <KeyValues
              items={[
                { label: "Version", value: <span className="font-mono text-secondary">{d.version}</span> },
                { label: "FIPS 140-3 mode", value: <Fips status={d.fips} /> },
                { label: "Database schema", value: `Migration ${d.schema}` },
                { label: "Connection settings", value: `Revision ${d.revision}` },
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
