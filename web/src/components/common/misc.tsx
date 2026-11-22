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
import { useTranslation } from "react-i18next";
import { i18n } from "@/lib/i18n";
import { Tip } from "@/components/ui/tooltip";
import type { CatalogItemType } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { fullDate, relative } from "@/lib/format";

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

/** What stands for an item whose icon isn't in the set: its kind. */
const kindIcons: Record<CatalogItemType, React.ElementType> = {
  vm_blueprint: Server,
  composite: Layers,
  awx_job_template: SquareTerminal,
  awx_workflow: Workflow,
  workflow: Workflow,
};

export function ItemIcon({ name, type, className }: { name: string; type?: CatalogItemType; className?: string }) {
  const I = icons[name] ?? (type ? kindIcons[type] : Server);
  return (
    <span
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-md border border-line bg-sunken text-muted",
        className,
      )}
      aria-hidden
    >
      <I className="size-4" strokeWidth={1.75} />
    </span>
  );
}

/** A catalog item's kind, in the language shown: call it while rendering. */
export const itemTypeLabel = (type: CatalogItemType) => i18n.t(`common:itemType.${type}`);

export function Time({ iso, className }: { iso: string; className?: string }) {
  useTranslation();
  return (
    <Tip content={fullDate(iso)}>
      <time dateTime={iso} className={cn("tnum whitespace-nowrap", className)}>
        {relative(iso)}
      </time>
    </Tip>
  );
}

export function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-secondary", className)}>{children}</span>;
}

export function CopyText({ value, className }: { value: string; className?: string }) {
  const { t } = useTranslation();
  const [done, setDone] = useState(false);
  return (
    <span className={cn("group inline-flex min-w-0 items-center gap-1", className)}>
      <span className="truncate font-mono text-secondary">{value}</span>
      <button
        type="button"
        aria-label={t("copy", { value })}
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
        "inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft text-badge font-semibold text-accent",
        className,
      )}
      aria-hidden
    >
      {init}
    </span>
  );
}
