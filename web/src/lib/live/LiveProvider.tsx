import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { apiUrl, rawFetch } from "@/lib/api/client";
import { useCurrentProject } from "@/lib/project";
import { applyEvent } from "./bridge";
import { LiveClient, type LiveStatus } from "./sse";

const StatusCtx = createContext<LiveStatus>("idle");

/** One event stream per tab, for the current project. Reopens when the project changes. */
export function LiveProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const router = useRouter();
  const { projectId } = useCurrentProject();
  const [status, setStatus] = useState<LiveStatus>("idle");

  useEffect(() => {
    const client = new LiveClient({
      url: () =>
        apiUrl(
          `/events?topics=requests,deployments,notifications,approvals&project_id=${encodeURIComponent(projectId)}`,
        ),
      fetch: rawFetch,
      onStatus: setStatus,
      onEvent: (event) =>
        applyEvent(qc, event, (n) => {
          const show =
            n.level === "error"
              ? toast.error
              : n.level === "warning"
                ? toast.warning
                : n.level === "success"
                  ? toast.success
                  : toast;
          const link = n.link;
          show(n.title, {
            description: n.body,
            action: link ? { label: "Open", onClick: () => void router.navigate({ to: link }) } : undefined,
          });
        }),
    });
    client.start();
    return () => client.stop();
  }, [qc, router, projectId]);

  return <StatusCtx.Provider value={status}>{children}</StatusCtx.Provider>;
}

/**
 * Follow one request live when it is outside the current project (an
 * approver or administrator looking at another project's request): the main
 * stream only carries the current project's requests.
 */
export function useRequestStream(requestId: string | undefined, enabled: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!requestId || !enabled) return;
    const client = new LiveClient({
      url: () => apiUrl(`/events?topics=request:${encodeURIComponent(requestId)}`),
      fetch: rawFetch,
      onEvent: (event) => applyEvent(qc, event),
    });
    client.start();
    return () => client.stop();
  }, [qc, requestId, enabled]);
}

export function useLiveStatus() {
  return useContext(StatusCtx);
}
