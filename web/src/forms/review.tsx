// A wizard's review step: what was entered on each page, as the requester
// would read it (labels rather than values, secrets masked), with a way back
// to each page.
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { i18n } from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import type { Option, UiColumn, UiField, UiWidget } from "@/lib/api/types";
import { toText } from "@/lib/utils";
import { fullDate, longDay } from "@/lib/format";
import { fileSize, isFileRef } from "./file";
import type { Part } from "./sections";
import { useFieldValues, type FormContext } from "./useFieldValues";

/** Shown, but nobody fills them in. */
const display: UiWidget[] = ["info", "image", "link"];
/** The value is found by searching the source for it. */
const searched: UiWidget[] = ["search", "user"];
const listed: UiWidget[] = ["multiselect", "checklist", "dual_list", "tags"];

export interface ReviewRow {
  label: string;
  value: ReactNode;
}

const isSecret = (ui: UiField) => ui.widget === "secret" || Boolean(ui.secret);

function blank(v: unknown): boolean {
  if (v === undefined || v === null || v === "") return true;
  if (Array.isArray(v)) return v.length === 0;
  return typeof v === "object" && !isFileRef(v) && Object.keys(v).length === 0;
}

function columnText(c: UiColumn, v: unknown): string {
  if (c.widget === "toggle") return i18n.t(v ? "forms:review.yes" : "forms:review.no");
  if (c.widget === "select") return c.options?.find((o) => o.value === String(v))?.label ?? toText(v);
  return toText(v);
}

/** A value as the review says it. */
export function enteredText(ui: UiField, v: unknown, options?: Option[]): string {
  const label = (x: unknown) => options?.find((o) => o.value === String(x))?.label ?? toText(x);
  if (ui.widget === "toggle") return i18n.t(v ? "forms:review.yes" : "forms:review.no");
  if (listed.includes(ui.widget) && Array.isArray(v)) return v.map(label).join(", ");
  if (ui.widget === "date" && typeof v === "string") return longDay(`${v}T00:00:00`);
  if (ui.widget === "datetime" && typeof v === "string") return fullDate(v);
  if (isFileRef(v)) return `${v.name} (${fileSize(v.size)})`;
  if (ui.widget === "datagrid" && Array.isArray(v)) return i18n.t("forms:review.rows", { count: v.length });
  if (ui.widget === "object" && typeof v === "object" && v !== null) {
    const obj = v as Record<string, unknown>;
    return (ui.columns ?? [])
      .filter((c) => !blank(obj[c.key]))
      .map((c) => `${c.label}: ${columnText(c, obj[c.key])}`)
      .join(", ");
  }
  if (ui.widget === "key_value" && typeof v === "object" && v !== null)
    return Object.entries(v as Record<string, unknown>)
      .map(([k, x]) => `${k}: ${toText(x)}`)
      .join(", ");
  return label(v);
}

function Entered({
  name,
  ui,
  value,
  values,
  ctx,
}: {
  name: string;
  ui: UiField;
  value: unknown;
  values: Record<string, unknown>;
  ctx: FormContext;
}) {
  const { t } = useTranslation("forms");
  const text = typeof value === "string" ? value : "";
  const search = searched.includes(ui.widget) ? { text, enabled: text !== "" } : undefined;
  // The labels its widget showed, from the same queries: cached, or asked once.
  const sourced = useFieldValues(name, ui, values, ctx, search);
  if (isSecret(ui)) {
    return blank(value) ? (
      <span className="text-muted">{t("review.notGiven")}</span>
    ) : (
      <>
        <span aria-hidden>••••</span>
        <span className="sr-only">{t("review.hidden")}</span>
      </>
    );
  }
  if (blank(value) && ui.widget !== "toggle") return <span className="text-muted">{t("review.notGiven")}</span>;
  return <>{enteredText(ui, value, ui.value_source ? sourced.data?.options : ui.options)}</>;
}

/** What was entered, page by page: each page's fields with their values, and Change to go back to it. */
export function Review({
  parts,
  values,
  ctx,
  leadingRows = [],
  onChange,
}: {
  parts: Part[];
  values: Record<string, unknown>;
  ctx: FormContext;
  /** Rows the first page lists before its fields, for what leads the form. */
  leadingRows?: ReviewRow[];
  onChange: (key: string) => void;
}) {
  const { t } = useTranslation("forms");
  return (
    <div className="flex flex-col divide-y divide-line">
      {parts.map((p, i) => {
        const fields = p.fields.filter((f) => !display.includes(f.ui.widget));
        const rows = i === 0 ? leadingRows : [];
        const id = `review-${p.section.key}`;
        return (
          <section key={p.section.key} aria-labelledby={id} className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
            <div className="flex items-center justify-between gap-2">
              <h3 id={id} className="text-body font-semibold">
                {p.section.title}
              </h3>
              <Button size="sm" variant="quiet" onClick={() => onChange(p.section.key)}>
                {t("review.change")}
                <span className="sr-only"> {p.section.title}</span>
              </Button>
            </div>
            {rows.length + fields.length === 0 ? (
              <p className="text-secondary text-muted">{t("review.nothing")}</p>
            ) : (
              <dl className="grid grid-cols-1 gap-x-4 gap-y-1.5 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
                {rows.map((r) => (
                  <div key={r.label} className="contents">
                    <dt className="text-secondary text-muted">{r.label}</dt>
                    <dd className="m-0 break-words">{r.value}</dd>
                  </div>
                ))}
                {fields.map(({ name, ui, state }) => (
                  <div key={name} className="contents">
                    <dt className="text-secondary text-muted">{ui.label}</dt>
                    <dd className="m-0 break-words">
                      <Entered
                        name={name}
                        ui={ui}
                        value={state.computed !== undefined ? state.computed : values[name]}
                        values={values}
                        ctx={ctx}
                      />
                    </dd>
                  </div>
                ))}
              </dl>
            )}
          </section>
        );
      })}
    </div>
  );
}
