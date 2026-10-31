// The System page (U1.5, ADR-0030), read only: the version and schema,
// rvav's processes and the settings revision each runs, the settings saved
// in Administration and how far each has applied, and the install
// settings, which are changed in the environment only.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, Clock, LoaderCircle, Lock } from "lucide-react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/utils";
import { roles } from "./Applying";
import { integrationTitle } from "./IntegrationSettings";

type S = components["schemas"];

const th = "border-b border-line px-4 py-2 font-medium";
const td = "border-b border-line px-4 py-2 align-top";

const applies: Record<S["SettingsRevision"]["applies"], string> = {
  next_use: "On next use",
  applying: "Applying",
  applied: "Applied",
};

function Applies({ value }: { value: S["SettingsRevision"]["applies"] }) {
  const Icon = value === "applying" ? LoaderCircle : value === "applied" ? CircleCheck : Clock;
  return (
    <span className="inline-flex items-center gap-1.5">
      <Icon className={value === "applying" ? "size-3.5 animate-spin text-warn" : "size-3.5 text-ok"} aria-hidden />
      {applies[value]}
    </span>
  );
}

function Processes({ system }: { system: S["System"] }) {
  const versions = new Set(system.processes.map((p) => p.version));
  return (
    <Panel title="Processes" flush>
      {versions.size > 1 && (
        <p role="status" className="border-b border-line px-4 py-2 text-xs text-warn">
          Processes run {versions.size} versions: an upgrade is under way, or a process was left behind.
        </p>
      )}
      {system.processes.length === 0 ? (
        <p className="px-4 py-3 text-xs text-muted">No process has recorded itself in the last 30 seconds.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]" aria-label="Processes">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={th}>
                  Process
                </th>
                <th scope="col" className={th}>
                  Version
                </th>
                <th scope="col" className={th}>
                  Settings revision
                </th>
                <th scope="col" className={th}>
                  State
                </th>
                <th scope="col" className={th}>
                  Started
                </th>
              </tr>
            </thead>
            <tbody>
              {system.processes.map((p) => (
                <tr key={p.id}>
                  <td className={td}>
                    <span className="font-medium">{roles[p.role]}</span>{" "}
                    <span className="font-mono text-xs text-faint">{p.host}</span>
                  </td>
                  <td className={`${td} font-mono text-xs`}>{p.version}</td>
                  <td className={td}>
                    {p.revision}
                    {p.revision < system.revision && <span className="ml-1.5 text-xs text-warn">earlier settings</span>}
                  </td>
                  <td className={td}>{states[p.state]}</td>
                  <td className={td} title={fullDate(p.started_at)}>
                    {relative(p.started_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function Revisions({ system }: { system: S["System"] }) {
  return (
    <Panel title="Settings saved in Administration" flush>
      {system.settings.length === 0 ? (
        <p className="px-4 py-3 text-xs text-muted">
          None yet: every setting comes from the environment or its default.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]" aria-label="Settings saved in Administration">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={th}>
                  Integration
                </th>
                <th scope="col" className={th}>
                  Revision
                </th>
                <th scope="col" className={th}>
                  Saved
                </th>
                <th scope="col" className={th}>
                  Applies
                </th>
              </tr>
            </thead>
            <tbody>
              {system.settings.map((r) => (
                <tr key={r.kind}>
                  <td className={`${td} font-medium`}>
                    <Link to="/admin/integrations" search={{ tab: r.kind }}>
                      {integrationTitle(r.kind)}
                    </Link>
                  </td>
                  <td className={td}>{r.revision}</td>
                  <td className={td}>
                    <span title={fullDate(r.updated_at)}>{relative(r.updated_at)}</span>
                    {r.updated_by && <span className="text-muted"> by {r.updated_by.name}</span>}
                  </td>
                  <td className={td}>
                    <Applies value={r.applies} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function Install({ system }: { system: S["System"] }) {
  return (
    <Panel title="Install settings" flush>
      <p className="border-b border-line px-4 py-2 text-xs text-muted">
        Set in the environment of each process, as the API answering reads them: change them there, then restart
        rvav&rsquo;s processes. A secret&rsquo;s value is never shown.
      </p>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[13px]" aria-label="Install settings">
          <thead>
            <tr className="label text-left">
              <th scope="col" className={th}>
                Setting
              </th>
              <th scope="col" className={th}>
                Value
              </th>
              <th scope="col" className={th}>
                From
              </th>
            </tr>
          </thead>
          <tbody>
            {system.install.map((s) => (
              <tr key={s.name}>
                <th scope="row" className={`${td} text-left font-mono text-xs font-normal`}>
                  {s.name}
                </th>
                <td className={`${td} font-mono text-xs break-all`}>
                  {s.value !== undefined && s.value !== "" ? (
                    <span>
                      {s.value}
                      {s.secret && (
                        <span className="ml-1.5 font-sans text-faint">
                          <Lock className="mr-0.5 inline size-3" aria-hidden />
                          password hidden
                        </span>
                      )}
                    </span>
                  ) : s.secret ? (
                    <span className="inline-flex items-center gap-1 font-sans">
                      <Lock className="size-3 text-faint" aria-hidden />
                      {s.set ? "Set" : "Not set"}
                    </span>
                  ) : (
                    <span className="font-sans text-faint">none</span>
                  )}
                </td>
                <td className={`${td} text-xs ${s.set ? "" : "text-muted"}`}>{s.set ? "Environment" : "Default"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

const states: Record<components["schemas"]["Process"]["state"], string> = {
  running: "Running",
  draining: "Finishing its running work",
  restarting: "Starting again",
};

export function SystemPage() {
  const system = useQuery({ ...adm.system(), refetchInterval: 10_000 });
  const d = system.data;
  return (
    <Page>
      <PageHeader
        title="System"
        meta={
          <>
            rvav as it runs now, read only. Settings saved in{" "}
            <Link to="/admin/integrations" search={{ tab: undefined }}>
              Integrations
            </Link>{" "}
            apply as they say; install settings are changed in the environment.
          </>
        }
      />
      {system.isPending && <SkeletonRows rows={6} cols={3} />}
      {system.isError && <ErrorState error={system.error} onRetry={() => void system.refetch()} />}
      {d && (
        <div className="flex flex-col gap-4">
          <Panel title="This install">
            <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-[13px]">
              <dt className="text-muted">Version</dt>
              <dd className="font-mono text-xs">{d.version}</dd>
              <dt className="text-muted">Database schema</dt>
              <dd>Migration {d.schema}</dd>
              <dt className="text-muted">Connection settings</dt>
              <dd>Revision {d.revision}</dd>
            </dl>
          </Panel>
          <Processes system={d} />
          <Revisions system={d} />
          <Install system={d} />
        </div>
      )}
    </Page>
  );
}
