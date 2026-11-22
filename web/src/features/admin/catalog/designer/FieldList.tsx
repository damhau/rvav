import { ArrowDown, ArrowUp, CircleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Panel } from "@/components/ui/panel";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { UiWidget } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import {
  addField,
  addSection,
  layoutChoices,
  layoutOf,
  moveField,
  moveSection,
  setLayout,
  widgetChoices,
  widgetLabel,
  type Def,
  type FormScope,
  type LayoutKind,
  type Problems,
} from "./model";

export type Selection =
  { kind: "field"; name: string } | { kind: "section"; key: string } | { kind: "validations" } | null;

/** Focuses an element once React has drawn the change (a move or a removal remounts it). */
export function focusLater(...ids: string[]) {
  requestAnimationFrame(() => {
    for (const id of ids) {
      const el = document.getElementById(id) as HTMLButtonElement | null;
      // A Button that turns disabled while focused is only aria-disabled until the focus leaves it.
      if (el && !el.disabled && el.getAttribute("aria-disabled") !== "true") return el.focus();
    }
  });
}

const tr = i18n.getFixedT(null, "admin-catalog");

/** Where a field is, said for a screen reader. */
function position(def: Def, name: string): string {
  const sections = def.ui_schema.layout.sections;
  const s = sections.find((x) => x.fields.includes(name));
  return s
    ? tr("designer.fields.position", {
        index: s.fields.indexOf(name) + 1,
        total: s.fields.length,
        section: s.title || s.key,
      })
    : tr("designer.fields.noSection");
}

function ProblemMark({ label }: { label: string }) {
  return <CircleAlert className="size-3.5 shrink-0 text-bad" aria-label={label} role="img" />;
}

export function FieldList({
  def,
  selection,
  onSelect,
  onChange,
  problems,
  scope = "item",
}: {
  def: Def;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onChange: (d: Def) => void;
  problems: Problems;
  scope?: FormScope;
}) {
  const { t } = useTranslation("admin-catalog");
  const layouts = layoutChoices(scope);
  const sections = def.ui_schema.layout.sections;
  const layout = layoutOf(def);
  const placed = new Set(sections.flatMap((s) => s.fields));
  const unplaced = Object.keys(def.ui_schema.fields).filter((n) => !placed.has(n));
  const validations = def.ui_schema.validations ?? [];
  const validating = selection?.kind === "validations";
  const currentSection =
    selection?.kind === "section"
      ? selection.key
      : sections.find((s) => selection?.kind === "field" && s.fields.includes(selection.name))?.key;
  const [announcement, setAnnouncement] = useState("");
  const move = (name: string, label: string, delta: -1 | 1) => {
    const next = moveField(def, name, delta);
    onChange(next);
    setAnnouncement(t("designer.fields.moved", { label, position: position(next, name) }));
    const [ahead, back] = delta < 0 ? ["up", "down"] : ["down", "up"];
    focusLater(`designer-${ahead}-${name}`, `designer-${back}-${name}`, `designer-field-${name}`);
  };
  const shift = (key: string, title: string, delta: -1 | 1) => {
    const next = moveSection(def, key, delta);
    onChange(next);
    const i = next.ui_schema.layout.sections.findIndex((x) => x.key === key);
    setAnnouncement(
      t(`designer.part.${layout}.moved`, { title, position: i + 1, total: next.ui_schema.layout.sections.length }),
    );
    const [ahead, back] = delta < 0 ? ["up", "down"] : ["down", "up"];
    focusLater(`designer-section-${ahead}-${key}`, `designer-section-${back}-${key}`, `designer-section-${key}`);
  };
  const isFirst = (si: number, i: number) => si === 0 && i === 0;
  const isLast = (si: number, i: number) => si === sections.length - 1 && i === sections[si]!.fields.length - 1;

  const fieldRow = (name: string, at?: { si: number; i: number }) => {
    const ui = def.ui_schema.fields[name];
    const label = ui?.label || name;
    const selected = selection?.kind === "field" && selection.name === name;
    return (
      <li key={name} className={cn("flex items-center gap-0.5 pr-1", selected && "bg-accent-soft")}>
        <button
          id={`designer-field-${name}`}
          type="button"
          aria-pressed={selected}
          onClick={() => onSelect({ kind: "field", name })}
          className="flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1.5 text-left text-body focus-visible:outline-2 focus-visible:outline-accent"
        >
          <span className="min-w-0 flex-1">
            <span className="block truncate">{label}</span>
            <span className="block truncate font-mono text-caption text-faint">
              {name} · {ui ? widgetLabel(ui.widget) : t("designer.fields.noUi")}
            </span>
          </span>
          {problems.fields[name] && <ProblemMark label={t("designer.fields.hasProblems", { label })} />}
        </button>
        {at && (
          <>
            <Button
              id={`designer-up-${name}`}
              size="icon"
              variant="quiet"
              aria-label={t("designer.fields.moveUp", { label })}
              disabled={isFirst(at.si, at.i)}
              onClick={() => move(name, label, -1)}
            >
              <ArrowUp className="size-3.5" />
            </Button>
            <Button
              id={`designer-down-${name}`}
              size="icon"
              variant="quiet"
              aria-label={t("designer.fields.moveDown", { label })}
              disabled={isLast(at.si, at.i)}
              onClick={() => move(name, label, 1)}
            >
              <ArrowDown className="size-3.5" />
            </Button>
          </>
        )}
      </li>
    );
  };

  return (
    <Panel title={t("designer.fields.title")} flush>
      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>
      <div className="flex flex-col">
        <div className="border-b border-line p-3">
          <Field
            label={t("designer.fields.layout")}
            htmlFor="designer-layout"
            hint={layouts.find((l) => l.value === layout)?.hint}
          >
            <Select
              id="designer-layout"
              value={layout}
              onChange={(k) => onChange(setLayout(def, k as LayoutKind))}
              options={layouts.map((l) => ({ value: l.value, label: l.label }))}
            />
          </Field>
        </div>
        {sections.map((s, si) => {
          const selected = selection?.kind === "section" && selection.key === s.key;
          return (
            <div
              key={s.key}
              role="group"
              aria-label={t(`designer.part.${layout}.group`, { title: s.title })}
              className="border-b border-line"
            >
              <div className={cn("flex items-center gap-0.5 pr-1", selected ? "bg-accent-soft" : "bg-sunken")}>
                <button
                  id={`designer-section-${s.key}`}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => onSelect({ kind: "section", key: s.key })}
                  className="flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1.5 text-left text-secondary font-semibold focus-visible:outline-2 focus-visible:outline-accent"
                >
                  <span className="truncate">{s.title || s.key}</span>
                  {problems.sections[si] && (
                    <ProblemMark label={t(`designer.part.${layout}.hasProblems`, { title: s.title })} />
                  )}
                </button>
                <Button
                  id={`designer-section-up-${s.key}`}
                  size="icon"
                  variant="quiet"
                  aria-label={t(`designer.part.${layout}.moveUp`, { title: s.title })}
                  disabled={si === 0}
                  onClick={() => shift(s.key, s.title, -1)}
                >
                  <ArrowUp className="size-3.5" />
                </Button>
                <Button
                  id={`designer-section-down-${s.key}`}
                  size="icon"
                  variant="quiet"
                  aria-label={t(`designer.part.${layout}.moveDown`, { title: s.title })}
                  disabled={si === sections.length - 1}
                  onClick={() => shift(s.key, s.title, 1)}
                >
                  <ArrowDown className="size-3.5" />
                </Button>
              </div>
              {s.fields.length === 0 ? (
                <p className="px-3 py-2 text-secondary text-faint">{t("designer.fields.empty")}</p>
              ) : (
                <ul>{s.fields.map((name, i) => fieldRow(name, { si, i }))}</ul>
              )}
            </div>
          );
        })}
        {unplaced.length > 0 && (
          <div role="group" aria-label={t(`designer.part.${layout}.notIn`)} className="border-b border-line">
            <p className="bg-sunken px-3 py-1.5 text-secondary font-semibold text-bad">
              {t(`designer.part.${layout}.notIn`)}
            </p>
            <ul>{unplaced.map((name) => fieldRow(name))}</ul>
          </div>
        )}
        <div className={cn("border-b border-line pr-1", validating && "bg-accent-soft")}>
          <button
            id="designer-validations"
            type="button"
            aria-pressed={validating}
            onClick={() => onSelect({ kind: "validations" })}
            className="flex w-full min-w-0 items-center gap-1.5 px-3 py-1.5 text-left text-secondary font-semibold focus-visible:outline-2 focus-visible:outline-accent"
          >
            <span className="min-w-0 flex-1 truncate">{t("designer.fields.validations")}</span>
            <span className="text-caption font-normal text-muted">
              {validations.length || t("designer.fields.noValidation")}
            </span>
            {Object.keys(problems.validations).length > 0 && (
              <ProblemMark label={t("designer.fields.validationsProblems")} />
            )}
          </button>
        </div>
        <div className="flex flex-col gap-2 p-3">
          <Field label={t("designer.fields.add")} htmlFor="designer-add-field">
            <Select
              id="designer-add-field"
              value=""
              placeholder={t("designer.fields.choose")}
              onChange={(w) => {
                const added = addField(def, w as UiWidget, currentSection);
                onChange(added.def);
                onSelect({ kind: "field", name: added.name });
              }}
              options={widgetChoices(scope)}
            />
          </Field>
          <Button
            id="designer-add-section"
            size="sm"
            onClick={() => {
              const added = addSection(def);
              onChange(added.def);
              onSelect({ kind: "section", key: added.key });
            }}
          >
            {t(`designer.part.${layout}.add`)}
          </Button>
        </div>
      </div>
    </Panel>
  );
}
