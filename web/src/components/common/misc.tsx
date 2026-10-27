import {
  Activity,
  Archive,
  Box,
  Check,
  Cloud,
  Container,
  Copy,
  Cpu,
  Database,
  FileText,
  GitBranch,
  Globe,
  HardDrive,
  KeyRound,
  Layers,
  Lock,
  Mail,
  Monitor,
  Network,
  Package,
  Server,
  Shield,
  SquareTerminal,
  Users,
  Workflow,
  Wrench,
} from "lucide-react";
import { useState } from "react";
import { Tip } from "@/components/ui/tooltip";
import type { CatalogItemType } from "@/lib/api/types";
import { cn, fullDate, relative } from "@/lib/utils";

/** Catalog item icons: the same set as the server's (core.CatalogIcons). */
export const icons: Record<string, React.ElementType> = {
  server: Server,
  monitor: Monitor,
  database: Database,
  "key-round": KeyRound,
  shield: Shield,
  globe: Globe,
  lock: Lock,
  users: Users,
  terminal: SquareTerminal,
  workflow: Workflow,
  box: Box,
  container: Container,
  "hard-drive": HardDrive,
  network: Network,
  cloud: Cloud,
  cpu: Cpu,
  package: Package,
  wrench: Wrench,
  mail: Mail,
  "file-text": FileText,
  archive: Archive,
  "git-branch": GitBranch,
  layers: Layers,
  activity: Activity,
};

export function ItemIcon({ name, className }: { name: string; className?: string }) {
  const I = icons[name] ?? Server;
  return (
    <span
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-md border border-line bg-sunken text-muted",
        className,
      )}
    >
      <I className="size-4" strokeWidth={1.75} />
    </span>
  );
}

export const itemTypeLabel: Record<CatalogItemType, string> = {
  vm_blueprint: "VM blueprint",
  awx_job_template: "AWX job template",
  awx_workflow: "AWX workflow",
  composite: "Composite",
};

export function Time({ iso, className }: { iso: string; className?: string }) {
  return (
    <Tip content={fullDate(iso)}>
      <time dateTime={iso} className={cn("tnum whitespace-nowrap", className)}>
        {relative(iso)}
      </time>
    </Tip>
  );
}

export function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12px]", className)}>{children}</span>;
}

export function CopyText({ value, className }: { value: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <span className={cn("group inline-flex min-w-0 items-center gap-1", className)}>
      <span className="truncate font-mono text-[12px]">{value}</span>
      <button
        type="button"
        aria-label={`Copy ${value}`}
        className="rounded-sm p-0.5 text-faint opacity-0 group-hover:opacity-100 hover:text-fg focus-visible:opacity-100"
        onClick={(e) => {
          e.stopPropagation();
          e.preventDefault();
          navigator.clipboard?.writeText(value).then(
            () => {
              setDone(true);
              setTimeout(() => setDone(false), 1200);
            },
            () => {},
          );
        }}
      >
        {done ? <Check className="size-3 text-ok" /> : <Copy className="size-3" />}
      </button>
    </span>
  );
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  const init = name
    .split(/\s+/)
    .map((p) => p[0])
    .join("")
    .slice(0, 2);
  return (
    <span
      className={cn(
        "inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft text-[10px] font-semibold text-accent",
        className,
      )}
      aria-hidden
    >
      {init}
    </span>
  );
}
