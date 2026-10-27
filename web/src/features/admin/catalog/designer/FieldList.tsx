import { ArrowDown, ArrowUp, CircleAlert } from "lucide-react";
import { Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { UiWidget } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { addField, addSection, moveField, moveSection, widgetLabels, type Def, type Problems } from "./model";

export type Selection = { kind: "field"; name: string } | { kind: "section"; key: string } | null;

const widgetOptions = Object.entries(widgetLabels).map(([value, label]) => ({ value, label }));

function ProblemMark({ label }: { label: string }) {
  return <CircleAlert className="size-3.5 shrink-0 text-bad" aria-label={label} role="img" />;
}

export function FieldList({
  def,
  selection,
  onSelect,
  onChange,
  problems,
}: {
  def: Def;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onChange: (d: Def) => void;
  problems: Problems;
}) {
  const sections = def.ui_schema.layout.sections;
  const placed = new Set(sections.flatMap((s) => s.fields));
  const unplaced = Object.keys(def.ui_schema.fields).filter((n) => !placed.has(n));
  const currentSection =
    selection?.kind === "section"
      ? selection.key
      : sections.find((s) => selection?.kind === "field" && s.fields.includes(selection.name))?.key;
  const isFirst = (si: number, i: number) => si === 0 && i === 0;
  const isLast = (si: number, i: number) => si === sections.length - 1 && i === sections[si]!.fields.length - 1;

  const fieldRow = (name: string, move?: { si: number; i: number }) => {
    const ui = def.ui_schema.fields[name];
    const label = ui?.label || name;
    const selected = selection?.kind === "field" && selection.name === name;
    return (
      <li key={name} className={cn("flex items-center gap-0.5 pr-1", selected && "bg-accent-soft")}>
        <button
          type="button"
          aria-pressed={selected}
          onClick={() => onSelect({ kind: "field", name })}
          className="flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1.5 text-left text-[13px] focus-visible:outline-2 focus-visible:outline-accent"
        >
          <span className="min-w-0 flex-1">
            <span className="block truncate">{label}</span>
            <span className="block truncate font-mono text-[11px] text-faint">
              {name} · {ui ? widgetLabels[ui.widget] : "no UI field"}
            </span>
          </span>
          {problems.fields[name] && <ProblemMark label={`${label} has problems`} />}
        </button>
        {move && (
          <>
            <Button
              size="icon"
              variant="ghost"
              aria-label={`Move ${label} up`}
              disabled={isFirst(move.si, move.i)}
              onClick={() => onChange(moveField(def, name, -1))}
            >
              <ArrowUp className="size-3.5" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              aria-label={`Move ${label} down`}
              disabled={isLast(move.si, move.i)}
              onClick={() => onChange(moveField(def, name, 1))}
            >
              <ArrowDown className="size-3.5" />
            </Button>
          </>
        )}
      </li>
    );
  };

  return (
    <Panel title="Fields" flush>
      <div className="flex flex-col">
        {sections.map((s, si) => {
          const selected = selection?.kind === "section" && selection.key === s.key;
          return (
            <div key={`${si}-${s.key}`} role="group" aria-label={`Section ${s.title}`} className="border-b border-line">
              <div className={cn("flex items-center gap-0.5 pr-1", selected ? "bg-accent-soft" : "bg-sunken")}>
                <button
                  type="button"
                  aria-pressed={selected}
                  onClick={() => onSelect({ kind: "section", key: s.key })}
                  className="flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1.5 text-left text-xs font-semibold focus-visible:outline-2 focus-visible:outline-accent"
                >
                  <span className="truncate">{s.title || s.key}</span>
                  {problems.sections[si] && <ProblemMark label={`Section ${s.title} has problems`} />}
                </button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`Move section ${s.title} up`}
                  disabled={si === 0}
                  onClick={() => onChange(moveSection(def, s.key, -1))}
                >
                  <ArrowUp className="size-3.5" />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`Move section ${s.title} down`}
                  disabled={si === sections.length - 1}
                  onClick={() => onChange(moveSection(def, s.key, 1))}
                >
                  <ArrowDown className="size-3.5" />
                </Button>
              </div>
              {s.fields.length === 0 ? (
                <p className="px-3 py-2 text-xs text-faint">No field yet.</p>
              ) : (
                <ul>{s.fields.map((name, i) => fieldRow(name, { si, i }))}</ul>
              )}
            </div>
          );
        })}
        {unplaced.length > 0 && (
          <div role="group" aria-label="Not in a section" className="border-b border-line">
            <p className="bg-sunken px-3 py-1.5 text-xs font-semibold text-bad">Not in a section</p>
            <ul>{unplaced.map((name) => fieldRow(name))}</ul>
          </div>
        )}
        <div className="flex flex-col gap-2 p-3">
          <Field label="Add a field" htmlFor="designer-add-field">
            <Select
              id="designer-add-field"
              value=""
              placeholder="Choose its widget…"
              onChange={(w) => {
                const added = addField(def, w as UiWidget, currentSection);
                onChange(added.def);
                onSelect({ kind: "field", name: added.name });
              }}
              options={widgetOptions}
            />
          </Field>
          <Button
            size="sm"
            onClick={() => {
              const added = addSection(def);
              onChange(added.def);
              onSelect({ kind: "section", key: added.key });
            }}
          >
            Add section
          </Button>
        </div>
      </div>
    </Panel>
  );
}
