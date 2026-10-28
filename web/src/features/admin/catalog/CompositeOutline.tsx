// A composite's steps as they will run, read from the YAML being edited:
// each group in order, a parallel group's steps side by side, what they
// run, use and undo, and what the server will refuse.
import { useMemo } from "react";
import { Badge } from "@/components/ui/badge";
import { readComposite, type CompositeStepView } from "./composite";
import { fromYaml, problemsOnLines } from "./definitionYaml";

const kindLabel: Record<CompositeStepView["kind"], string> = {
  item: "Item",
  awx: "AWX",
  workflow: "Workflow",
  "?": "?",
};

function StepCard({ s }: { s: CompositeStepView }) {
  return (
    <li className="min-w-[180px] flex-1 rounded-md border border-line bg-surface p-2 text-xs">
      <div className="flex items-center gap-1.5">
        <Badge tone="outline">{kindLabel[s.kind]}</Badge>
        <span className="font-mono font-medium">{s.key || "(no key)"}</span>
      </div>
      {s.title && <div className="mt-0.5 text-muted">{s.title}</div>}
      <div className="mt-0.5 font-mono text-faint">{s.target || "—"}</div>
      {s.when && <div className="mt-0.5 font-mono text-faint">when {s.when}</div>}
      {s.uses.length > 0 && <div className="mt-0.5 text-muted">Uses {s.uses.join(", ")}</div>}
      {s.undo && <div className="mt-0.5 text-muted">Undone by {s.undo}</div>}
    </li>
  );
}

export function CompositeOutline({ text }: { text: string }) {
  const view = useMemo(() => {
    const parsed = fromYaml(text);
    const blueprint = parsed.def?.blueprint;
    if (blueprint?.kind !== "composite") return null;
    const v = readComposite(blueprint);
    return { ...v, lines: problemsOnLines(text, v.problems) };
  }, [text]);
  if (!view) return null;
  return (
    <section aria-label="Composite steps" className="mt-3 flex flex-col gap-2">
      <h3 className="text-xs font-medium">Steps, in the order they run</h3>
      {view.groups.length === 0 && <p className="text-xs text-muted">No step yet: list them under blueprint.steps.</p>}
      <ol className="flex flex-col gap-2">
        {view.groups.map((group, g) => (
          <li key={g} className="flex items-start gap-2">
            <span className="mt-2 w-5 shrink-0 text-right text-xs text-faint">{g + 1}</span>
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              {group.length > 1 && <span className="text-[11px] text-faint">At the same time</span>}
              <ul className="flex flex-wrap gap-2">
                {group.map((s) => (
                  <StepCard key={s.pointer} s={s} />
                ))}
              </ul>
            </div>
          </li>
        ))}
      </ol>
      {view.lines.length > 0 && (
        <ul aria-label="Problems in the steps" className="flex flex-col gap-0.5 text-xs text-warn">
          {view.lines.map((p, i) => (
            <li key={i}>
              Line {p.line}: {p.message}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
