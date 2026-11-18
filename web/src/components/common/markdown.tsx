import ReactMarkdown, { type Components } from "react-markdown";
import { cn } from "@/lib/utils";

interface MdNode {
  type: string;
  depth?: number;
  children?: MdNode[];
}

/** Moves the text's headings so its first level is `level`, below the page's own. */
function headingsFrom(level: number) {
  return () => (tree: MdNode) => {
    const found: MdNode[] = [];
    const walk = (n: MdNode) => {
      if (n.type === "heading") found.push(n);
      n.children?.forEach(walk);
    };
    walk(tree);
    if (found.length === 0) return;
    const top = Math.min(...found.map((h) => h.depth ?? 1));
    for (const h of found) h.depth = Math.min(6, (h.depth ?? 1) - top + level);
  };
}

const heading = "text-body font-semibold";

const components: Components = {
  h1: ({ node: _n, children, ...p }) => (
    <h1 className={heading} {...p}>
      {children}
    </h1>
  ),
  h2: ({ node: _n, children, ...p }) => (
    <h2 className={heading} {...p}>
      {children}
    </h2>
  ),
  h3: ({ node: _n, children, ...p }) => (
    <h3 className={heading} {...p}>
      {children}
    </h3>
  ),
  h4: ({ node: _n, children, ...p }) => (
    <h4 className={heading} {...p}>
      {children}
    </h4>
  ),
  h5: ({ node: _n, children, ...p }) => (
    <h5 className={heading} {...p}>
      {children}
    </h5>
  ),
  h6: ({ node: _n, children, ...p }) => (
    <h6 className={heading} {...p}>
      {children}
    </h6>
  ),
  ul: ({ node: _n, ...p }) => <ul className="list-disc space-y-1 pl-5" {...p} />,
  ol: ({ node: _n, ...p }) => <ol className="list-decimal space-y-1 pl-5" {...p} />,
  a: ({ node: _n, children, ...p }) => (
    <a className="text-accent underline underline-offset-2" target="_blank" rel="noreferrer" {...p}>
      {children}
    </a>
  ),
  code: ({ node: _n, ...p }) => <code className="rounded-sm bg-sunken px-1 font-mono text-secondary" {...p} />,
  pre: ({ node: _n, ...p }) => (
    <pre
      className="overflow-x-auto rounded-md bg-sunken px-3 py-2 font-mono text-secondary [&_code]:bg-transparent [&_code]:p-0"
      {...p}
    />
  ),
  blockquote: ({ node: _n, ...p }) => <blockquote className="border-l-2 border-line-strong pl-3 text-muted" {...p} />,
  hr: () => <hr className="border-line" />,
};

/**
 * Markdown written by an administrator, such as a catalog item's
 * description: CommonMark without its HTML (left out), images or unsafe
 * links (react-markdown's own check), headings from `headingLevel` down.
 */
export function Markdown({
  children,
  headingLevel = 3,
  className,
}: {
  children: string;
  /** The level of the text's first headings: one below the section it is in. */
  headingLevel?: number;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-3 text-body break-words", className)}>
      <ReactMarkdown
        skipHtml
        disallowedElements={["img"]}
        remarkPlugins={[headingsFrom(headingLevel)]}
        components={components}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
