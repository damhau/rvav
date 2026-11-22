import { useId, type Ref } from "react";
import { cn } from "@/lib/utils";

interface HeaderProps {
  title?: React.ReactNode;
  /** One line on what the panel holds or why it matters. */
  description?: React.ReactNode;
  actions?: React.ReactNode;
  /** The heading's level: 2 on a page, 3 inside a section. */
  level?: 2 | 3;
  /** Lets the heading take the focus, for a step a person moves to (a wizard's page). */
  titleRef?: Ref<HTMLHeadingElement>;
}

function Header({
  id,
  title,
  description,
  actions,
  level = 2,
  titleRef,
  className,
}: HeaderProps & { id: string; className?: string }) {
  const H = level === 3 ? "h3" : "h2";
  return (
    <div className={cn("flex flex-wrap items-start justify-between gap-x-4 gap-y-2", className)}>
      <div className="flex min-w-0 flex-col gap-0.5">
        <H
          id={id}
          ref={titleRef}
          tabIndex={titleRef ? -1 : undefined}
          className={cn("text-section font-semibold", titleRef && "outline-none")}
        >
          {title}
        </H>
        {description && <p className="max-w-[72ch] text-secondary text-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/**
 * A bordered box for one thing on a page: a header (title, description,
 * actions), a body, and an optional footer. Flush bodies (a table) touch the
 * border; others are padded.
 */
export function Panel({
  title,
  description,
  actions,
  level,
  titleRef,
  footer,
  children,
  className,
  flush,
  as: Box = "section",
  describedBy,
}: HeaderProps & {
  /** Help on the left, buttons on the right: what a settings form saves with. */
  footer?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  flush?: boolean;
  /** `fieldset` for a section of a form: a group of fields its title names. */
  as?: "section" | "fieldset";
  /** The id of what explains the whole group, such as a problem with it. */
  describedBy?: string;
}) {
  const id = useId();
  return (
    <Box
      className={cn("m-0 min-w-0 rounded-lg border border-line bg-surface p-0", className)}
      aria-labelledby={title ? id : undefined}
      aria-describedby={describedBy}
    >
      {title && (
        <Header
          id={id}
          title={title}
          description={description}
          actions={actions}
          level={level}
          titleRef={titleRef}
          className={cn("px-4 pt-3.5", flush ? "border-b border-line pb-3" : "pb-0")}
        />
      )}
      <div className={flush ? "" : "p-4"}>{children}</div>
      {footer && (
        <div className="flex flex-wrap items-center justify-end gap-2 rounded-b-lg border-t border-line bg-sunken/50 px-4 py-3 text-secondary text-muted">
          {footer}
        </div>
      )}
    </Box>
  );
}

/** A titled part of a page without a box, for content that has its own (cards, tables) or needs none. */
export function Section({
  title,
  description,
  actions,
  level,
  children,
  className,
}: HeaderProps & { title: React.ReactNode; children: React.ReactNode; className?: string }) {
  const id = useId();
  return (
    <section className={cn("flex min-w-0 flex-col gap-3", className)} aria-labelledby={id}>
      <Header id={id} title={title} description={description} actions={actions} level={level} />
      {children}
    </section>
  );
}
