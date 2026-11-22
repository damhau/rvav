// Saved connection settings being applied (ADR-0030): each of rvav's
// processes finishes the work it runs, exits and is started again with them,
// by Compose, Kubernetes or systemd. Shown in the connection's panel until
// every process runs with them.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, LoaderCircle, Clock } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { adm } from "@/lib/query";

type Process = components["schemas"]["Process"];

/** What a process of rvav does, as its role says. */
export const roleName = (role: Process["role"]) => i18n.t(`admin:applying.role.${role}`);

/** How often the processes are asked while a change applies. */
export const APPLY_POLL_MS = 1000;

export function Applying({ revision }: { revision: number }) {
  const { t } = useTranslation("admin");
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
      aria-label={t("applying.label")}
      className="flex flex-col gap-2 rounded-md border border-line p-3 text-secondary"
    >
      <p role="status" className="font-medium">
        {done ? t("applying.done") : procs.isError ? t("applying.apiRestarting") : t("applying.running")}
      </p>
      {items.length > 0 && (
        <ul className="flex flex-col gap-1" aria-label={t("applying.processes")}>
          {items.map((p) => {
            const applied = p.revision >= revision;
            return (
              <li key={p.id} className="flex items-center gap-1.5">
                {applied ? (
                  <CircleCheck className="size-3.5 shrink-0 text-ok" aria-hidden />
                ) : p.state !== "running" ? (
                  <LoaderCircle className="size-3.5 shrink-0 animate-spin text-warn" aria-hidden />
                ) : (
                  <Clock className="size-3.5 shrink-0 text-faint" aria-hidden />
                )}
                <span className="font-medium">{roleName(p.role)}</span>
                <span className="font-mono text-faint">{p.host}</span>
                <span>
                  {applied
                    ? t("applying.applied")
                    : p.state === "draining"
                      ? t("applying.draining")
                      : p.state === "restarting"
                        ? t("applying.restarting")
                        : t("applying.earlier")}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
