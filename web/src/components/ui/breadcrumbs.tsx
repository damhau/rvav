import { ChevronRight } from "lucide-react";
import { Children, isValidElement } from "react";

/** Where a page sits: links to the pages above it, from the top-level page down; the page's own title follows. */
export function Breadcrumbs({ children }: { children: React.ReactNode }) {
  return (
    <nav aria-label="Breadcrumb">
      <ol className="flex flex-wrap items-center gap-x-1 gap-y-0.5 text-secondary text-muted [&_a]:rounded-sm [&_a]:underline-offset-2 [&_a]:hover:text-fg [&_a]:hover:underline">
        {/* Each crumb points on to the next, the last to the page's title below. */}
        {Children.toArray(children).map((crumb, i) => (
          <li key={isValidElement(crumb) ? crumb.key : i} className="flex items-center gap-1">
            {crumb}
            <ChevronRight aria-hidden className="size-3.5 shrink-0 text-faint" />
          </li>
        ))}
      </ol>
    </nav>
  );
}
