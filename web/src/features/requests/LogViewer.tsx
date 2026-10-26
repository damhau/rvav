import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { api, call } from "@/lib/api/client";
import type { LogLine, Step } from "@/lib/api/types";
import { keys } from "@/lib/query";
import { cn } from "@/lib/utils";

const kindClass: Record<NonNullable<LogLine["kind"]>, string> = {
  plain: "text-log-fg",
  header: "font-medium text-log-fg",
  ok: "text-log-ok",
  changed: "text-log-changed",
  failed: "text-log-bad",
  skipped: "text-log-dim",
  unreachable: "text-log-bad",
  recap: "font-medium text-log-fg",
};

/**
 * AWX job output, read incrementally. `request.log` events bump the log head
 * in the query cache; this component then fetches from its last offset.
 */
export function LogViewer({ requestId, step }: { requestId: string; step: Step }) {
  // A fresh stream per step, so offsets and lines never mix between jobs.
  return <LogStream key={`${requestId}:${step.id}`} requestId={requestId} step={step} />;
}

function LogStream({ requestId, step }: { requestId: string; step: Step }) {
  const head = useQuery({
    queryKey: keys.requests.logHead(requestId, step.id),
    queryFn: () => 0,
    enabled: false,
    initialData: 0,
  });
  const [lines, setLines] = useState<LogLine[]>([]);
  const [complete, setComplete] = useState(false);
  const [error, setError] = useState(false);
  const offset = useRef(0);
  // Pulls run one after another, each from the offset the previous one reached.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const alive = useRef(true);
  const box = useRef<HTMLDivElement>(null);
  const follow = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    queue.current = queue.current
      .then(async () => {
        for (;;) {
          if (!alive.current) return;
          const chunk = await call(
            api.GET("/requests/{requestId}/steps/{stepId}/log", {
              params: { path: { requestId, stepId: step.id }, query: { offset: offset.current } },
            }),
          );
          if (!alive.current) return;
          offset.current = chunk.next_offset;
          if (chunk.lines.length) setLines((l) => [...l, ...chunk.lines]);
          setComplete(chunk.complete);
          setError(false);
          if (chunk.lines.length === 0 || chunk.complete) return;
        }
      })
      .catch(() => {
        if (alive.current) setError(true);
      });
  }, [requestId, step.id, step.status, head.data]);

  useEffect(() => {
    const el = box.current;
    if (el && follow.current) el.scrollTop = el.scrollHeight;
  }, [lines]);

  return (
    <div
      ref={box}
      // Focusable so keyboard users can scroll the output.
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
      tabIndex={0}
      role="log"
      aria-label={`Output of ${step.title}`}
      onScroll={(e) => {
        const el = e.currentTarget;
        follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
      className="h-[380px] overflow-auto bg-log-bg px-3 py-2.5 font-mono text-[11.5px] leading-[1.55]"
    >
      {lines.length === 0 && !error && (
        <p className="text-log-dim">
          {step.status === "pending" ? "The job hasn't started yet." : "Waiting for output…"}
        </p>
      )}
      {lines.map((l) => (
        <div key={l.n} className={cn("break-words whitespace-pre-wrap", kindClass[l.kind ?? "plain"])}>
          {l.text || " "}
        </div>
      ))}
      {error && <p className="text-log-bad">Couldn't load the job output. It will be retried on the next update.</p>}
      {complete && lines.length > 0 && <p className="mt-1 text-log-dim">— end of output —</p>}
    </div>
  );
}
