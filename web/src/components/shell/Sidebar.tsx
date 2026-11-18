import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useId, type ReactNode } from "react";
import { Tip } from "@/components/ui/tooltip";
import { useLiveStatus } from "@/lib/live/LiveProvider";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { cn } from "@/lib/utils";
import { adminMenu, mainPages } from "./nav";

const row = "relative flex items-center rounded-md hover:bg-sunken hover:text-fg [&_svg]:shrink-0";
const mainRow = "h-[30px] text-fg [&_svg]:size-4 [&_svg]:text-muted";
const adminRow = "h-7 text-muted [&_svg]:size-[15px] [&_svg]:text-faint";
const wide = "gap-2.5 px-2 text-body";
const narrow = "w-full justify-center";
const active = "bg-surface font-medium text-fg shadow-[0_0_0_1px_var(--line)] hover:bg-surface [&_svg]:text-accent!";

function liveText(status: ReturnType<typeof useLiveStatus>) {
  return status === "open"
    ? "Live updates on"
    : status === "reconnecting"
      ? "Reconnecting…"
      : status === "connecting"
        ? "Connecting…"
        : "Live updates off";
}

function LiveIndicator({ rail }: { rail: boolean }) {
  const status = useLiveStatus();
  const text = liveText(status);
  const dot = (
    <span
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        status === "open" ? "bg-ok" : status === "reconnecting" ? "bg-warn" : "bg-line-strong",
      )}
      aria-hidden
    />
  );
  if (rail) {
    return (
      <Tip content={text} side="right">
        <span className="flex h-5 items-center justify-center" role="status">
          {dot}
          <span className="sr-only">{text}</span>
        </span>
      </Tip>
    );
  }
  return (
    <span className="flex items-center gap-1.5" role="status">
      {dot}
      {text}
    </span>
  );
}

/**
 * The main menu, then the administration pages someone may open, in groups.
 * On the rail (between a phone's width and a desktop's) it shows icons only.
 */
export function Sidebar({ rail = false, onNavigate }: { rail?: boolean; onNavigate?: () => void }) {
  const { projectId, me } = useCurrentProject();
  const groups = adminMenu(me);
  const running = useInfiniteQuery(q.requests({ projectId, status: ["running", "queued", "cancelling"], limit: 50 }));
  const runningCount = running.data?.pages.reduce((n, p) => n + p.items.length, 0) ?? 0;
  const waiting = useQuery(q.approvals(["pending"])).data?.total ?? 0;
  const linkProps = { activeProps: { className: active }, onClick: onNavigate } as const;
  const id = useId();
  const adminId = `${id}-admin`;

  const extra = (key: (typeof mainPages)[number]["key"]) => {
    if (key === "requests" && runningCount > 0) {
      return rail ? (
        <>
          <span className="absolute top-1.5 right-2 size-1.5 rounded-full bg-run" aria-hidden />
          <span className="sr-only">{runningCount} running</span>
        </>
      ) : (
        <span className="tnum ml-auto text-caption text-run">{runningCount} running</span>
      );
    }
    if (key === "approvals" && waiting > 0) {
      return (
        <span
          className={cn(
            "tnum rounded-full bg-warn-soft text-center font-medium text-warn",
            rail ? "absolute -top-0.5 right-0 min-w-4 px-1 text-badge" : "ml-auto min-w-[18px] px-1.5 text-caption",
          )}
          aria-label={`${waiting} waiting for you`}
        >
          {waiting}
        </span>
      );
    }
    return null;
  };

  const label = (text: string) =>
    rail ? <span className="sr-only">{text}</span> : <span className="min-w-0 truncate">{text}</span>;
  const withTip = (text: string, link: ReactNode) =>
    rail ? (
      <Tip content={text} side="right">
        {link}
      </Tip>
    ) : (
      link
    );

  return (
    <nav aria-label="Main" className="flex h-full flex-col">
      <div className={cn("pt-3.5 pb-4", rail ? "flex justify-center px-2" : "px-3")}>
        <Link
          to="/"
          activeOptions={{ exact: true }}
          className={cn("flex items-center gap-2 rounded-md py-0.5 text-fg", rail ? "justify-center px-1" : "px-1.5")}
          onClick={onNavigate}
        >
          <span
            aria-hidden
            className="flex size-[22px] shrink-0 items-center justify-center rounded-sm bg-fg font-mono text-caption font-medium text-bg"
          >
            rv
          </span>
          <span className={rail ? "sr-only" : "text-body font-semibold tracking-[-0.01em]"}>rvav</span>
          <span className={rail ? "sr-only" : "ml-auto text-caption text-faint"}>GVA</span>
        </Link>
      </div>

      <div
        className={cn(
          "flex min-h-0 flex-1 [scrollbar-width:thin] flex-col gap-4 overflow-x-hidden overflow-y-auto py-1",
          rail ? "px-2" : "px-3",
        )}
      >
        <ul className="flex flex-col gap-0.5">
          {mainPages.map((p) => (
            <li key={p.key}>
              {withTip(
                p.label,
                <Link
                  to={p.to}
                  activeOptions={p.to === "/" ? { exact: true } : undefined}
                  className={cn(row, mainRow, rail ? narrow : wide)}
                  {...linkProps}
                >
                  <p.icon aria-hidden />
                  {label(p.label)}
                  {extra(p.key)}
                </Link>,
              )}
            </li>
          ))}
        </ul>

        {groups.length > 0 && (
          <div
            role="group"
            aria-labelledby={adminId}
            className={cn("flex flex-col border-t border-line", rail ? "gap-2 pt-2" : "gap-3 pt-3.5")}
          >
            <span id={adminId} className={rail ? "sr-only" : "px-2 text-secondary font-semibold text-fg"}>
              Administration
            </span>
            {groups.map((g, i) => (
              <div key={g.key} className={cn("flex flex-col", rail ? i > 0 && "border-t border-line pt-2" : "gap-1")}>
                <span id={`${adminId}-${g.key}`} className={rail ? "sr-only" : "px-2 text-caption text-faint"}>
                  {g.label}
                </span>
                <ul aria-labelledby={`${adminId}-${g.key}`} className="flex flex-col gap-px">
                  {g.pages.map((p) => (
                    <li key={p.to}>
                      {withTip(
                        p.label,
                        <Link to={p.to} className={cn(row, adminRow, rail ? narrow : wide)} {...linkProps}>
                          <p.icon aria-hidden />
                          {label(p.label)}
                        </Link>,
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </div>

      <div
        className={cn(
          "flex flex-col gap-1 border-t border-line pt-2.5 pb-3 text-caption text-faint",
          rail ? "mx-2 items-center" : "mx-3 px-2",
        )}
      >
        <LiveIndicator rail={rail} />
      </div>
    </nav>
  );
}
