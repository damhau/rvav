// Renders a request form from its JSON Schema and UI schema: sections, widgets,
// server-resolved values with dependencies, the server's rule results and
// field errors, in the form's layout (one page, a wizard or tabs).
import { useEffect, useImperativeHandle, useMemo, useRef, useState, type ReactNode, type Ref } from "react";
import { Panel } from "@/components/ui/panel";
import type { FieldStates, RequestForm } from "@/lib/api/types";
import { fieldControl } from "./attention";
import { withDefaults } from "./defaults";
import { FieldGrid, formParts, partShows, sectionFields, type GridProps } from "./sections";
import { FormTabs } from "./tabs";
import { REVIEW, Wizard, type WizardOptions } from "./wizard";
import type { FormContext } from "./useFieldValues";

export type { PageCheck, PageInputs, WizardOptions } from "./wizard";

/** What a page holding the form asks of it. */
export interface FormHandle {
  /**
   * Moves the focus to an input's control, first showing the page or tab it
   * is on. `control` finds it when it isn't a form field's (the environment).
   */
  focus(name: string, control?: () => HTMLElement | null): void;
}

export interface FormRendererProps {
  form: RequestForm;
  /** What the requester gave: a field without a key here shows its default. */
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
  onBlur?: (name: string) => void;
  errors?: Record<string, string>;
  ctx: FormContext;
  /**
   * The server's rule results (the preview's `fields`): which fields show,
   * are required or read-only, computed values and defaults. Until they
   * come, the schema's.
   */
  fieldStates?: FieldStates;
  /** Rendered at the start of the first section (the environment picker). */
  leading?: ReactNode;
  /** The inputs `leading` asks for, which the first page or tab holds. */
  leadingFields?: string[];
  /** The wizard's check, review step and submit, for a form whose layout is a wizard. */
  wizard?: WizardOptions;
  /** A section's key or a field's name: its page or tab shows (the designer's selection). */
  showing?: string;
  /** Finds an input's control, when some aren't form fields'. */
  controlOf?: (name: string) => HTMLElement | null;
  ref?: Ref<FormHandle>;
}

/** The inputs the form shows a field for, in the order they show. */
export function shownFields(form: RequestForm, states?: FieldStates): string[] {
  return form.ui_schema.layout.sections.flatMap((s) => sectionFields(form, s.fields, states).map((f) => f.name));
}

/** The section a name is: a section's key, or a field in it. */
function sectionOf(form: RequestForm, name: string): string | undefined {
  const sections = form.ui_schema.layout.sections;
  return (sections.find((s) => s.key === name) ?? sections.find((s) => s.fields.includes(name)))?.key;
}

export function FormRenderer({
  form,
  values,
  onChange,
  onBlur,
  errors = {},
  ctx,
  fieldStates,
  leading,
  leadingFields = [],
  wizard,
  showing,
  controlOf = fieldControl,
  ref,
}: FormRendererProps) {
  const kind = form.ui_schema.layout.kind || "page";
  const parts = formParts(form, fieldStates);
  const shown = useMemo(
    () => withDefaults(values, fieldStates, form.initial_values),
    [values, fieldStates, form.initial_values],
  );
  const grid: GridProps = { form, values: shown, onChange, onBlur, errors, ctx };
  // The page or tab shown; `n` renders again when it is shown anew, for what runs after.
  const [view, setView] = useState<{ key?: string; n: number }>({ key: showing && sectionOf(form, showing), n: 0 });
  const after = useRef<(() => void) | undefined>(undefined);
  useEffect(() => {
    const then = after.current;
    after.current = undefined;
    then?.();
  }, [view]);
  const show = (key: string, then?: () => void) => {
    after.current = then;
    setView((v) => ({ key, n: v.n + 1 }));
  };
  const [shownFor, setShownFor] = useState(showing);
  if (showing !== shownFor) {
    setShownFor(showing);
    const key = showing && sectionOf(form, showing);
    if (key) setView((v) => ({ key, n: v.n }));
  }

  const partOf = (name: string): string | undefined => {
    if (leadingFields.includes(name)) return parts.find((p) => partShows(p, Boolean(leading)))?.section.key;
    return sectionOf(form, name) ?? (kind === "wizard" ? REVIEW : undefined);
  };
  useImperativeHandle(ref, () => ({
    focus(name, control = () => controlOf(name)) {
      const key = kind === "page" ? undefined : partOf(name);
      if (key) show(key, () => control()?.focus());
      else control()?.focus();
    },
  }));

  if (kind === "wizard") {
    return (
      <Wizard
        grid={grid}
        parts={parts}
        leading={leading}
        leadingFields={leadingFields}
        options={wizard ?? {}}
        current={view.key}
        show={show}
        control={controlOf}
        fieldStates={fieldStates}
      />
    );
  }
  if (kind === "tabs") {
    return (
      <FormTabs
        grid={grid}
        parts={parts}
        leading={leading}
        leadingFields={leadingFields}
        current={view.key}
        show={show}
      />
    );
  }
  return (
    <div className="flex flex-col gap-4">
      {parts.map((p) => {
        if (!partShows(p, Boolean(leading))) return null;
        return (
          <Panel key={p.section.key} as="fieldset" title={p.section.title} description={p.section.description}>
            <FieldGrid {...grid} section={p.section} fields={p.fields} leading={p.index === 0 ? leading : undefined} />
          </Panel>
        );
      })}
    </div>
  );
}
