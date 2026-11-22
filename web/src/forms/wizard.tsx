// The wizard layout (F1.3): one section per page, Next checks the page before
// it goes on, pages whose fields are all hidden are left out, and a review
// step lists what was entered before submit. Focus moves to the new page's
// heading on Next and Back.
import { ArrowLeft, ArrowRight, CircleAlert, LoaderCircle } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/ui/panel";
import { StepMark, type StepState } from "@/components/ui/step-mark";
import type { FieldStates, RequestForm } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { Review, type ReviewRow } from "./review";
import { FieldGrid, formParts, partInputs, partShows, problemsIn, type GridProps, type Part } from "./sections";

/** The key of the review step, which no section has: section keys are names. */
export const REVIEW = "$review";

/** What a check of a page found. */
export interface PageCheck {
  /** The page's inputs with a problem, in its order. */
  problems: string[];
  /** The rule results the check saw: they say which pages come next. */
  fields?: FieldStates;
}

/** A page's inputs under the given rule results, which say which of its fields show. */
export type PageInputs = (fields?: FieldStates) => string[];

export interface WizardOptions {
  /** Checks a page's inputs before Next leaves it. Without it, Next doesn't check. */
  check?: (inputs: PageInputs) => Promise<PageCheck>;
  /**
   * Checks the whole form before the review step shows (its validations,
   * ADR-0043): true holds the wizard, which then shows what it refused.
   */
  checkAll?: () => Promise<boolean>;
  /** On the review step, below what was entered: what the request asks besides the form. */
  review?: ReactNode;
  /** The review step's action, beside Back: the submit button. */
  submit?: ReactNode;
  /** Rows the review lists first, for what leads the form. */
  leadingRows?: ReviewRow[];
  /** Why the form can't go on yet, such as a file still sending: Next waits, and this says why. */
  holding?: string;
}

export function Wizard({
  grid,
  parts,
  leading,
  leadingFields,
  options,
  current,
  show,
  control,
  fieldStates,
}: {
  grid: GridProps;
  parts: Part[];
  leading?: ReactNode;
  leadingFields: string[];
  options: WizardOptions;
  current?: string;
  show: (key: string, then?: () => void) => void;
  control: (name: string) => HTMLElement | null;
  fieldStates?: FieldStates;
}) {
  const { t } = useTranslation("forms");
  const form: RequestForm = grid.form;
  const shown = parts.filter((p) => partShows(p, Boolean(leading)) || p.section.key === current);
  const steps: { key: string; title: string; part?: Part }[] = [
    ...shown.map((p) => ({ key: p.section.key, title: p.section.title, part: p })),
    { key: REVIEW, title: t("review.title") },
  ];
  const at = Math.max(
    0,
    steps.findIndex((s) => s.key === current),
  );
  const step = steps[at]!;
  const headings = useRef(new Map<string, HTMLHeadingElement>());
  const [checking, setChecking] = useState(false);
  const busy = useRef(false);

  const go = (key: string) => show(key, () => headings.current.get(key)?.focus());
  const titleRef = (key: string) => (el: HTMLHeadingElement | null) => {
    if (el) headings.current.set(key, el);
    else headings.current.delete(key);
  };
  const next = async () => {
    const part = step.part;
    if (!part || busy.current || options.holding) return;
    let states = fieldStates;
    busy.current = true;
    setChecking(true);
    try {
      if (options.check) {
        try {
          const result = await options.check((fields) =>
            partInputs(formParts(form, fields)[part.index] ?? part, leadingFields),
          );
          const first = result.problems[0];
          if (first) {
            const key = part.section.key;
            return show(key, () => (control(first) ?? headings.current.get(key))?.focus());
          }
          states = result.fields ?? states;
        } catch {
          // A check that can't run doesn't hold the page: the server checks on submit.
        }
      }
      const following = formParts(form, states)
        .slice(part.index + 1)
        .find((p) => partShows(p, false));
      if (!following && options.checkAll) {
        try {
          if (await options.checkAll()) return;
        } catch {
          // As above: submit runs the validations again.
        }
      }
      go(following?.section.key ?? REVIEW);
    } finally {
      busy.current = false;
      setChecking(false);
    }
  };
  const back = () => {
    const previous = steps[at - 1];
    if (previous) go(previous.key);
  };
  const of = (i: number) => t("wizard.stepOf", { n: i + 1, total: steps.length });

  return (
    <div className="flex flex-col gap-4">
      <ol
        aria-label={t("wizard.steps")}
        className="flex flex-wrap gap-x-5 gap-y-2 rounded-lg border border-line bg-surface px-4 py-3"
      >
        {steps.map((s, i) => {
          const state: StepState = i < at ? "done" : i === at ? "now" : "later";
          const bad = s.part ? problemsIn(s.part, leadingFields, grid.errors) > 0 : false;
          return (
            <li
              key={s.key}
              aria-current={state === "now" ? "step" : undefined}
              className="flex min-w-0 items-center gap-2"
            >
              <StepMark n={i + 1} state={state} />
              <span className={cn("text-secondary", state === "now" ? "font-medium text-fg" : "text-muted")}>
                {s.title}
                {state === "done" && <span className="sr-only">, {t("wizard.done")}</span>}
              </span>
              {bad && (
                <>
                  <CircleAlert className="size-3.5 shrink-0 text-bad" aria-hidden />
                  <span className="sr-only">, {t("wizard.needsAttention")}</span>
                </>
              )}
            </li>
          );
        })}
      </ol>
      {shown.map((p) => {
        const i = steps.findIndex((s) => s.key === p.section.key);
        return (
          <div key={p.section.key} data-part={p.section.key} hidden={p.section.key !== step.key}>
            <Panel
              as="fieldset"
              title={
                <>
                  {p.section.title}
                  <span className="sr-only">, {of(i).toLowerCase()}</span>
                </>
              }
              description={p.section.description}
              actions={
                <span aria-hidden className="text-caption text-muted">
                  {of(i)}
                </span>
              }
              titleRef={titleRef(p.section.key)}
            >
              <FieldGrid
                {...grid}
                section={p.section}
                fields={p.fields}
                leading={p.index === 0 ? leading : undefined}
              />
            </Panel>
          </div>
        );
      })}
      <div data-part={REVIEW} hidden={step.key !== REVIEW} className="flex flex-col gap-4">
        <Panel
          title={
            <>
              {t("review.title")}
              <span className="sr-only">, {of(steps.length - 1).toLowerCase()}</span>
            </>
          }
          description={t("review.lead")}
          actions={
            <span aria-hidden className="text-caption text-muted">
              {of(steps.length - 1)}
            </span>
          }
          titleRef={titleRef(REVIEW)}
        >
          {step.key === REVIEW && (
            <Review
              parts={shown.filter((p) => partShows(p, Boolean(leading)))}
              values={grid.values}
              ctx={grid.ctx}
              leadingRows={options.leadingRows}
              onChange={go}
            />
          )}
        </Panel>
        {options.review}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {at > 0 && (
          <Button onClick={back}>
            <ArrowLeft aria-hidden />
            {t("wizard.back")}
          </Button>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {options.holding && (
            <span role="status" className="text-caption text-muted">
              {options.holding}
            </span>
          )}
          {step.key === REVIEW ? (
            options.submit
          ) : (
            <Button variant="primary" onClick={() => void next()} disabled={Boolean(options.holding)}>
              {checking && <LoaderCircle className="animate-spin" aria-hidden />}
              {t("wizard.next")}
              <ArrowRight aria-hidden />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
