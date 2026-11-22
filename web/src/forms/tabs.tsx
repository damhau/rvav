// The tabs layout (F1.3): one section per tab, in the tablist pattern. A tab
// whose fields are all hidden is left out; one with a problem shown says so.
import { CircleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { FieldGrid, partShows, problemsIn, type GridProps, type Part } from "./sections";

export function FormTabs({
  grid,
  parts,
  leading,
  leadingFields,
  current,
  show,
}: {
  grid: GridProps;
  parts: Part[];
  leading?: ReactNode;
  leadingFields: string[];
  current?: string;
  show: (key: string) => void;
}) {
  const { t } = useTranslation("forms");
  const shown = parts.filter((p) => partShows(p, Boolean(leading)));
  const first = shown[0];
  if (!first) return null;
  const value = shown.some((p) => p.section.key === current) ? current! : first.section.key;
  return (
    <Tabs value={value} onValueChange={show} className="flex flex-col rounded-lg border border-line bg-surface">
      <TabsList aria-label={t("sections")} className="px-4 pt-2">
        {shown.map((p) => {
          const problems = problemsIn(p, leadingFields, grid.errors);
          return (
            <TabsTrigger key={p.section.key} value={p.section.key} data-part-tab={p.section.key}>
              {p.section.title}
              {problems > 0 && (
                <>
                  <CircleAlert className="size-3.5 shrink-0 text-bad" aria-hidden />
                  <span className="sr-only">, {t("needsAttention", { count: problems })}</span>
                </>
              )}
            </TabsTrigger>
          );
        })}
      </TabsList>
      {shown.map((p) => (
        // Kept mounted when not shown: a tab's values and widgets' state stay, and a problem's control is there to focus.
        <TabsContent
          key={p.section.key}
          value={p.section.key}
          forceMount
          hidden={p.section.key !== value}
          data-part={p.section.key}
          className="flex flex-col gap-4 p-4"
        >
          {p.section.description && <p className="max-w-[72ch] text-secondary text-muted">{p.section.description}</p>}
          <FieldGrid {...grid} section={p.section} fields={p.fields} leading={p.index === 0 ? leading : undefined} />
        </TabsContent>
      ))}
    </Tabs>
  );
}
