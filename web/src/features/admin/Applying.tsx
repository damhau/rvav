// Saved connection settings being applied (ADR-0030): each of rvav's
// processes finishes the work it runs, exits and is started again with them,
// by Compose, Kubernetes or systemd. Shown in the connection's panel until
// every process runs with them.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, LoaderCircle, Clock } from "lucide-react";
import { useEffect } from "react";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";

type Process = components["schemas"]["Process"];

const roles: Record<Process["role"], string> = {
  api: "API",
  worker: "Worker",
  sync: "Inventory sync",
  dev: "rvav dev",
};

/** How often the processes are asked while a change applies. */
export const APPLY_POLL_MS = 1000;

export function Applying({ revision }: { revision: number }) {
  const qc = useQueryClient();
  const procs = useQuery({
    ...adm.processes(),
    // Until every process runs with them; the API itself starts again, so
    // a failed poll is tried again too.
    refetchInterval: (q) =>
      q.state.status === "error" || (q.state.data?.items ?? []).some((p) => p.revision < revision)
        ? APPLY_POLL_MS
        : false,
    retry: false,
  });
  const items = procs.data?.items ?? [];
  const done = procs.isSuccess && !items.some((p) => p.revision < revision);
  useEffect(() => {
    if (done) void qc.invalidateQueries({ queryKey: ["admin", "integrations"] });
  }, [done, qc]);

  return (
    <section
      aria-label="Applying the settings"
      className="flex flex-col gap-2 rounded-md border border-line p-3 text-xs"
    >
      <p role="status" className="font-medium">
        {done
          ? "Applied: every process runs with these settings."
          : procs.isError
            ? "rvav's API is starting again with these settings…"
            : "Applying: each process finishes the work it runs, then starts again with these settings."}
      </p>
      {items.length > 0 && (
        <ul className="flex flex-col gap-1" aria-label="rvav's processes">
          {items.map((p) => {
            const applied = p.revision >= revision;
            return (
              <li key={p.id} className="flex items-center gap-1.5">
                {applied ? (
                  <CircleCheck className="size-3.5 shrink-0 text-ok" aria-hidden />
                ) : p.state === "draining" ? (
                  <LoaderCircle className="size-3.5 shrink-0 animate-spin text-warn" aria-hidden />
                ) : (
                  <Clock className="size-3.5 shrink-0 text-faint" aria-hidden />
                )}
                <span className="font-medium">{roles[p.role]}</span>
                <span className="font-mono text-faint">{p.host}</span>
                <span>
                  {applied
                    ? "Applied"
                    : p.state === "draining"
                      ? "Finishing its running work"
                      : "Runs with the earlier settings"}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
