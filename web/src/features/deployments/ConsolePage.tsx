import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink, Maximize, Power, RotateCcw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { api, call, isApiError } from "@/lib/api/client";
import type { ConsoleSession, VmResource } from "@/lib/api/types";
import { q } from "@/lib/query";

// VMware's HTML console client (WMKS), loaded from where the organisation
// serves it (ADR-0012). Only what this page uses.
interface Wmks {
  connect(url: string): void;
  disconnect(): void;
  destroy(): void;
  sendCAD(): void;
  enterFullScreen(): void;
  register(event: string, handler: (event: unknown, data: { state?: string }) => void): Wmks;
}
declare global {
  interface Window {
    WMKS?: {
      createWMKS(id: string, options: Record<string, unknown>): Wmks;
      CONST: {
        Events: { CONNECTION_STATE_CHANGE: string };
        ConnectionState: { CONNECTED: string; DISCONNECTED: string };
      };
    };
  }
}

const loaded = new Map<string, Promise<void>>();

function loadScript(src: string): Promise<void> {
  let p = loaded.get(src);
  if (!p) {
    p = new Promise<void>((resolve, reject) => {
      const s = document.createElement("script");
      s.src = src;
      s.onload = () => resolve();
      s.onerror = () => reject(new Error(`Could not load ${src}`));
      document.head.appendChild(s);
    });
    loaded.set(src, p);
  }
  return p;
}

function wsUrl(path: string) {
  const { protocol, host } = globalThis.location;
  return `${protocol === "https:" ? "wss" : "ws"}://${host}${path}`;
}

type State = "loading" | "connecting" | "connected" | "disconnected" | "failed";

function WebConsole({ session, onReconnect }: { session: ConsoleSession; onReconnect: () => void }) {
  const web = session.web!;
  const wmks = useRef<Wmks | null>(null);
  const [state, setState] = useState<State>("loading");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        for (const src of web.sdk_urls) await loadScript(src);
        if (cancelled) return;
        const W = window.WMKS;
        if (!W) throw new Error("The console client did not load (no WMKS).");
        setState("connecting");
        wmks.current = W.createWMKS("rvav-wmks", {}).register(W.CONST.Events.CONNECTION_STATE_CHANGE, (_e, data) => {
          if (data.state === W.CONST.ConnectionState.CONNECTED) setState("connected");
          if (data.state === W.CONST.ConnectionState.DISCONNECTED) setState("disconnected");
        });
        wmks.current.connect(wsUrl(web.url));
      } catch (e) {
        if (!cancelled) {
          setState("failed");
          setError(e instanceof Error ? e.message : String(e));
        }
      }
    })();
    return () => {
      cancelled = true;
      wmks.current?.destroy();
      wmks.current = null;
    };
  }, [web]);

  const label: Record<State, string> = {
    loading: "Loading the console client…",
    connecting: "Connecting…",
    connected: "Connected",
    disconnected: "Disconnected",
    failed: "Could not open the console",
  };
  return (
    <Panel
      title="Web console"
      flush
      actions={
        <span className="flex items-center gap-2">
          <span className="text-[11px] text-faint" role="status">
            {label[state]}
          </span>
          <Button size="sm" disabled={state !== "connected"} onClick={() => wmks.current?.sendCAD()}>
            Ctrl+Alt+Del
          </Button>
          <Button size="sm" disabled={state !== "connected"} onClick={() => wmks.current?.enterFullScreen()}>
            <Maximize /> Full screen
          </Button>
          <Button size="sm" disabled={state === "loading" || state === "connecting"} onClick={onReconnect}>
            <RotateCcw /> Reconnect
          </Button>
        </span>
      }
    >
      {error && <p className="border-b border-line px-4 py-2.5 text-xs text-bad">{error}</p>}
      <div id="rvav-wmks" className="aspect-[16/10] w-full bg-black" />
    </Panel>
  );
}

export function ConsolePage({ deploymentId, resourceId }: { deploymentId: string; resourceId: string }) {
  const dep = useQuery(q.deployment(deploymentId));
  const vm = dep.data?.resources.find((r): r is VmResource => r.id === resourceId && r.type === "vsphere.vm");
  const params = { path: { deploymentId, resourceId } };
  // Tickets work once: each open, VMRC click and reconnect asks for a new one.
  const session = useMutation({
    mutationFn: () => call(api.POST("/deployments/{deploymentId}/resources/{resourceId}/console", { params })),
  });
  const vmrc = useMutation({
    mutationFn: () => call(api.POST("/deployments/{deploymentId}/resources/{resourceId}/console", { params })),
    onSuccess: (s) => {
      globalThis.location.assign(s.vmrc_url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : "Could not open VMRC"),
  });
  const { mutate: open } = session;
  const opened = useRef(false);
  useEffect(() => {
    if (opened.current) return;
    opened.current = true;
    open();
  }, [open]);

  const name = vm?.name ?? dep.data?.name;
  return (
    <Page>
      <PageHeader
        crumbs={
          <>
            <Link to="/deployments">Deployments</Link> /
            <Link to="/deployments/$deploymentId" params={{ deploymentId }}>
              {dep.data?.name ?? "…"}
            </Link>
            / Console
          </>
        }
        title={name ? `Console · ${name}` : <Skeleton className="h-5 w-48" />}
        meta={vm && <span className="font-mono text-[11px]">{vm.vm.moref}</span>}
        actions={
          <Button onClick={() => vmrc.mutate()} disabled={vmrc.isPending}>
            <ExternalLink /> Open in VMRC
          </Button>
        }
      />
      {session.isError ? (
        <ErrorState
          error={session.error}
          onRetry={() => session.mutate()}
          className="rounded-lg border border-line bg-surface"
        />
      ) : !session.data ? (
        <Skeleton className="aspect-[16/10] w-full" />
      ) : session.data.web ? (
        <WebConsole session={session.data} onReconnect={() => session.mutate()} />
      ) : (
        <Panel title="Web console">
          <p className="flex items-start gap-2 text-xs text-muted">
            <Power className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <span>
              The web console is not set up here: an administrator provides VMware&apos;s HTML console client
              (RVAV_WMKS_SDK_URLS). Open the console in VMware Remote Console instead; it needs VMRC installed and
              access to vCenter and the ESXi hosts.
            </span>
          </p>
        </Panel>
      )}
    </Page>
  );
}
