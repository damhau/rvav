import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ChartColumn, House, LayoutGrid, List, Server, SquareCheckBig } from "lucide-react";
import { useLiveStatus } from "@/lib/live/LiveProvider";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { cn } from "@/lib/utils";

const item =
  "flex h-[30px] items-center gap-2.5 rounded-md px-2 text-[13px] text-muted hover:bg-sunken hover:text-fg [&_svg]:size-[15px] [&_svg]:shrink-0";
const active = "bg-surface font-medium text-fg shadow-[0_0_0_1px_var(--line)] hover:bg-surface";

interface Roles {
  admin: boolean;
  catalogAdmin: boolean;
  auditor: boolean;
  projectAdmin: boolean;
}

// The administration pages, each for those who may use it (ADR-0020); the
// server decides again.
const admin: [string, string, (r: Roles) => boolean][] = [
  ["catalog-items", "Catalog items", (r) => r.admin || r.catalogAdmin],
  ["day2-actions", "Day-2 actions", (r) => r.admin],
  ["policies", "Policies", (r) => r.admin || r.auditor],
  ["projects", "Projects", (r) => r.admin || r.auditor || r.projectAdmin],
  ["integrations", "Integrations", (r) => r.admin],
  ["audit", "Audit log", (r) => r.admin || r.auditor || r.projectAdmin],
];

function LiveIndicator() {
  const status = useLiveStatus();
  const text =
    status === "open"
      ? "Live updates on"
      : status === "reconnecting"
        ? "Reconnecting…"
        : status === "connecting"
          ? "Connecting…"
          : "Live updates off";
  return (
    <span className="flex items-center gap-1.5" role="status">
      <span
        className={cn(
          "size-1.5 rounded-full",
          status === "open" ? "bg-ok" : status === "reconnecting" ? "bg-warn" : "bg-line-strong",
        )}
        aria-hidden
      />
      {text}
    </span>
  );
}

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { projectId, me } = useCurrentProject();
  const roles: Roles = {
    admin: me.global_roles.includes("admin"),
    catalogAdmin: me.global_roles.includes("catalog-admin"),
    auditor: me.global_roles.includes("auditor"),
    projectAdmin: me.projects.some((p) => p.role === "project-admin"),
  };
  const adminPages = admin.filter(([, , show]) => show(roles));
  const running = useInfiniteQuery(q.requests({ projectId, status: ["running", "queued", "cancelling"], limit: 50 }));
  const runningCount = running.data?.pages.reduce((n, p) => n + p.items.length, 0) ?? 0;
  const waiting = useQuery(q.approvals(["pending"])).data?.total ?? 0;
  const linkProps = { activeProps: { className: active }, onClick: onNavigate } as const;

  return (
    <nav aria-label="Main" className="flex h-full flex-col gap-5 px-3 py-3.5">
      <Link to="/" className="flex items-center gap-2 px-1.5 py-0.5 text-fg" onClick={onNavigate}>
        <span
          aria-hidden
          className="flex size-[22px] items-center justify-center rounded-sm bg-fg font-mono text-[11px] font-medium text-bg"
        >
          rv
        </span>
        <span className="text-sm font-semibold tracking-[-0.01em]">rvav</span>
        <span className="ml-auto font-mono text-[10px] text-faint">GVA</span>
      </Link>

      <ul className="flex flex-col gap-0.5">
        <li>
          <Link to="/" className={item} activeOptions={{ exact: true }} {...linkProps}>
            <House aria-hidden /> Overview
          </Link>
        </li>
        <li>
          <Link to="/catalog" className={item} {...linkProps}>
            <LayoutGrid aria-hidden /> Catalog
          </Link>
        </li>
        <li>
          <Link to="/deployments" className={item} {...linkProps}>
            <Server aria-hidden /> Deployments
          </Link>
        </li>
        <li>
          <Link to="/requests" className={item} {...linkProps}>
            <List aria-hidden /> Requests
            {runningCount > 0 && <span className="ml-auto font-mono text-[11px] text-run">{runningCount} running</span>}
          </Link>
        </li>
        <li>
          <Link to="/approvals" className={item} {...linkProps}>
            <SquareCheckBig aria-hidden /> Approvals
            {waiting > 0 && (
              <span
                className="ml-auto min-w-[18px] rounded-full bg-warn-soft px-1.5 text-center font-mono text-[11px] font-medium text-warn"
                aria-label={`${waiting} waiting for you`}
              >
                {waiting}
              </span>
            )}
          </Link>
        </li>
        <li>
          <Link to="/reports" className={item} {...linkProps}>
            <ChartColumn aria-hidden /> Reports
          </Link>
        </li>
      </ul>

      {adminPages.length > 0 && (
        <div className="flex flex-col gap-0.5">
          <span className="label px-2 pb-1">Administration</span>
          <ul className="flex flex-col gap-0.5">
            {adminPages.map(([section, label]) => (
              <li key={section}>
                <Link to="/admin/$section" params={{ section }} className={cn(item, "h-7")} {...linkProps}>
                  {label}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="mt-auto flex flex-col gap-1 border-t border-line px-2 pt-2.5 text-[11px] text-faint">
        <LiveIndicator />
        <span className="font-mono">vc01.corp.example</span>
      </div>
    </nav>
  );
}
