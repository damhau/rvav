// An item's topology beside its YAML (ADR-0032), as Kestra puts the code
// and the graph side by side: a VM item's components, networks, segments,
// groups and rules, or a composite's steps, drawn from the editor's text
// each time it changes. Loaded only with the Definition tab, so React Flow
// stays out of the other pages.
//
// Text that doesn't parse, or a blueprint the canvas doesn't draw, keeps
// the last graph that did and says why above it: the reader keeps their
// place (zoom, selection, focus) through a keystroke that breaks the YAML
// for a moment, and the graph catches up when it parses again.
//
// The composite designer (composite/CompositeDesigner) draws through it,
// with its own problems and the canvas's selection, actions and details.
import { TriangleAlert } from "lucide-react";
import { useDeferredValue, useMemo, useState } from "react";
import { LineCounter, parseDocument } from "yaml";
import { addProblems, blueprintGraph, Canvas, type CanvasGraph, type CanvasProps } from "@/components/canvas";
import type { FieldError } from "@/lib/api/types";
import { problemsOnLines } from "./definitionYaml";

export interface Drawn {
  graph: CanvasGraph | null;
  /** Why there is no graph for this text. */
  reason?: string;
}

/** The graph of the blueprint in a definition's YAML, or why there is none. */
export function drawDefinition(text: string): Drawn {
  const lc = new LineCounter();
  const doc = parseDocument(text, { lineCounter: lc });
  const broken = doc.errors[0];
  if (broken) return { graph: null, reason: `The YAML doesn't parse at line ${lc.linePos(broken.pos[0]).line}` };
  let def: unknown;
  try {
    def = doc.toJS();
  } catch (e) {
    return { graph: null, reason: `The YAML doesn't parse: ${e instanceof Error ? e.message : String(e)}` };
  }
  const blueprint = def && typeof def === "object" ? (def as Record<string, unknown>).blueprint : undefined;
  if (!blueprint || typeof blueprint !== "object") return { graph: null, reason: "The definition has no blueprint" };
  const graph = blueprintGraph(blueprint);
  if (!graph) {
    const kind = (blueprint as Record<string, unknown>).kind;
    return {
      graph: null,
      reason: kind ? `A blueprint of kind ${JSON.stringify(kind)} isn't drawn` : "The blueprint has no kind",
    };
  }
  return { graph };
}

export interface DefinitionGraphProps {
  /** The editor's text, as typed. */
  text: string;
  /** The item's name, for the canvas's label. */
  name: string;
  composite: boolean;
  /** More problems with a text than the structural ones, shown on their nodes (the designer's and the server's). */
  problemsFor?: (text: string) => FieldError[];
  /** The designer's selection, actions and details. */
  canvas?: Pick<CanvasProps, "selected" | "onSelect" | "renderDetails" | "actions">;
}

export default function DefinitionGraph({ text, name, composite, problemsFor, canvas }: DefinitionGraphProps) {
  // Typing stays quick: the graph is drawn again once React has time.
  const current = useDeferredValue(text);
  const drawn = useMemo(() => {
    const d = drawDefinition(current);
    return d.graph && problemsFor ? { ...d, graph: addProblems(d.graph, problemsFor(current)) } : d;
  }, [current, problemsFor]);
  // The last graph drawn, for the moments the text has none.
  const [kept, setKept] = useState<CanvasGraph | null>(drawn.graph);
  if (drawn.graph && drawn.graph !== kept) setKept(drawn.graph);
  const graph = drawn.graph ?? kept;
  const lines = useMemo(
    () => (drawn.graph ? problemsOnLines(current, drawn.graph.problems) : []),
    [drawn.graph, current],
  );
  const what = composite ? "steps" : "topology";
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-xs font-medium">{composite ? "Steps, in the order they run" : "Topology"}</h3>
      {drawn.reason && (
        <p className="flex items-start gap-1.5 text-xs text-warn">
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span>
            {drawn.reason}
            {graph ? ": the graph shows the definition as it last parsed." : "."}
          </span>
        </p>
      )}
      {graph && <Canvas graph={graph} label={`${composite ? "Steps" : "Topology"} of ${name}`} {...canvas} />}
      {lines.length > 0 && (
        <ul aria-label={`Problems in the ${what}`} className="flex flex-col gap-0.5 text-xs text-warn">
          {lines.map((p, i) => (
            <li key={i}>
              Line {p.line}: {p.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
