// A composite request's run on its graph (D1.4, ADR-0032): the requested
// version's steps, each with its live status from the event stream. A node
// selected shows its steps (and the page its AWX output); the failed one,
// selected at first, its error and the request's Retry. Its timeline is the
// list view: it says all the graph does, in order, and more.
//
// Loaded lazily, like the topology on the request form: React Flow stays out
// of the other pages.
import { useMemo, useState } from "react";
import { Canvas, type CanvasGraph, type CanvasNode, type CanvasStatus, type CanvasView } from "@/components/canvas";
import { NodeFacts } from "@/components/canvas/CanvasList";
import { statusLabel } from "@/components/canvas/graph";
import { NodeIcon, StatusIcon } from "@/components/canvas/nodes";
import { Timeline } from "@/components/common/timeline";
import type { ServiceRequest, Step } from "@/lib/api/types";
import { FailureActions } from "./FailureActions";
import { failedNode, flatten, runStatus, runSteps } from "./run";

export interface RequestRunProps {
  request: ServiceRequest;
  graph: CanvasGraph;
  steps: Step[];
  /** The selected node; the page shows its AWX output. */
  selected: string | null;
  onSelect: (id: string | null) => void;
  /** The AWX job whose output the page shows. */
  logStep: string | null;
  onOpenLog: (step: Step) => void;
}

export default function RequestRun({ request, graph, steps, selected, onSelect, logStep, onOpenLog }: RequestRunProps) {
  const [view, setView] = useState<CanvasView>("graph");
  const byNode = useMemo(() => runSteps(graph, steps), [graph, steps]);
  const status = useMemo(() => runStatus(graph, byNode), [graph, byNode]);
  const failed = failedNode(request, byNode);
  const failure = request.status === "failed" && request.error ? <FailureActions request={request} /> : null;

  return (
    <>
      <Canvas
        graph={graph}
        label={`Run of ${request.number}`}
        status={status}
        selected={selected}
        onSelect={onSelect}
        view={view}
        onViewChange={setView}
        list={{
          label: "Timeline",
          content: (
            <div>
              <Timeline steps={steps} onOpenLog={onOpenLog} selectedLogStep={logStep} />
              {failure}
            </div>
          ),
        }}
        renderDetails={(node) => (
          <RunDetails
            node={node}
            status={status[node.id]}
            steps={byNode.get(node.id) ?? []}
            logStep={logStep}
            onOpenLog={onOpenLog}
          >
            {node.id === failed && failure}
          </RunDetails>
        )}
      />
      {view === "graph" && selected !== failed && failure}
    </>
  );
}

/** A node's part of the run: what it is, its steps as the timeline shows them, their AWX jobs' output. */
function RunDetails({
  node,
  status,
  steps,
  logStep,
  onOpenLog,
  children,
}: {
  node: CanvasNode;
  status?: CanvasStatus;
  steps: Step[];
  logStep: string | null;
  onOpenLog: (step: Step) => void;
  children?: React.ReactNode;
}) {
  // The timeline offers the output of its own steps' jobs; these are deeper (a VM step's post-configuration).
  const deeper = flatten(steps.flatMap((s) => s.children)).filter((s) => s.has_log && s.status !== "pending");
  return (
    <div
      aria-label={`Selected: ${node.title}`}
      role="region"
      className="flex flex-col gap-2 rounded-md border border-line bg-surface p-3 text-xs"
    >
      <div className="flex items-center gap-1.5 text-[13px] font-medium">
        <NodeIcon node={node} />
        {node.badge && (
          <>
            <span className="text-[10px] tracking-wide text-faint uppercase">{node.badge}</span>{" "}
          </>
        )}
        {node.title}
        {status && (
          <span className="ml-auto flex items-center gap-1 text-xs font-normal text-muted">
            <StatusIcon status={status} />
            <span aria-hidden>{statusLabel(status)}</span>
          </span>
        )}
      </div>
      <div>
        <NodeFacts node={node} />
      </div>
      {steps.length > 0 ? (
        <Timeline steps={steps} onOpenLog={onOpenLog} selectedLogStep={logStep} />
      ) : (
        <p className="text-muted">Nothing has run here yet.</p>
      )}
      {deeper.length > 0 && (
        <ul className="flex flex-col gap-1">
          {deeper.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => onOpenLog(s)}
                aria-pressed={logStep === s.id}
                className="text-left text-xs font-medium text-accent hover:underline"
              >
                {logStep === s.id ? "Showing the output of" : "Show the output of"} {s.title}
              </button>
            </li>
          ))}
        </ul>
      )}
      {children}
    </div>
  );
}
