import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import type { Step } from "@/lib/api/types";
import { q } from "@/lib/query";

/** What the server shows of a secret input (ADR-0038). */
const masked = "••••";

function Value({ v }: { v: unknown }) {
  if (v === masked)
    return (
      <>
        <span aria-hidden>{masked}</span>
        <span className="sr-only">Secret, masked</span>
      </>
    );
  if (v === null || v === undefined) return <span className="text-muted">null</span>;
  if (v === "") return <span className="text-muted">empty</span>;
  if (typeof v === "object")
    return (
      <pre className="m-0 font-mono text-secondary break-words whitespace-pre-wrap">{JSON.stringify(v, null, 2)}</pre>
    );
  return <>{typeof v === "string" ? v : JSON.stringify(v)}</>;
}

function Fields({
  title,
  values,
  empty,
  note,
}: {
  title: string;
  values: Record<string, unknown> | null | undefined;
  empty: string;
  note?: string;
}) {
  const id = useId();
  const entries = Object.entries(values ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <section aria-labelledby={id} className="flex flex-col gap-1.5">
      <h3 id={id} className="text-body font-medium">
        {title}
      </h3>
      {note && entries.length > 0 && <p className="text-secondary text-muted">{note}</p>}
      {entries.length > 0 ? (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5">
          {entries.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="font-mono text-secondary text-muted">{k}</dt>
              <dd className="m-0 min-w-0 font-mono text-secondary break-words">
                <Value v={v} />
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="text-secondary text-muted">{empty}</p>
      )}
    </section>
  );
}

/** What a step was given and what it gave (ADR-0057), secrets masked by the server. */
export function StepDataView({ requestId, step }: { requestId: string; step: Step }) {
  const data = useQuery(q.stepData(requestId, step.id, `${step.status}:${step.finished_at ?? ""}`));
  if (data.isPending) return <Skeleton className="h-24" />;
  if (data.isError) return <ErrorState error={data.error} onRetry={() => void data.refetch()} className="p-0" />;
  const d = data.data;
  const unfinished = step.status === "pending" || step.status === "running";
  // A composite's own step: later steps read what it gave as steps.<key>.
  const key = /^step:([a-z][a-z0-9_]*)$/.exec(step.key)?.[1];
  return (
    <div className="flex flex-col gap-4">
      <Fields title="Inputs" values={d.inputs} empty="It was given nothing it keeps." />
      {d.outputs_hidden ? (
        <section className="flex flex-col gap-1.5">
          <h3 className="text-body font-medium">Outputs</h3>
          <p className="text-secondary text-muted">
            Only the people it asks, and those who read every approval, read the answer.
          </p>
        </section>
      ) : (
        <Fields
          title="Outputs"
          values={d.outputs}
          empty={unfinished ? "Nothing yet: it hasn't finished." : "It gave nothing."}
          note={key ? `Later steps read them as steps.${key}.<name>.` : undefined}
        />
      )}
    </div>
  );
}
