// The catalog's Git repository (C1.4, ADR-0029): its state on the catalog
// page, with the files that need someone; and each item's file on its
// page: its last commit, the repository's change, a conflict to settle.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { GitBranch } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { AdminCatalogItem, CatalogGitFile, CatalogItemGit, DiffLine } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { cn, relative } from "@/lib/utils";

type CatalogGitStatus = components["schemas"]["CatalogGitStatus"];

type State = CatalogItemGit["state"];

export const gitStateLabel: Record<State, string> = {
  synced: "In Git",
  waiting: "Waiting to be committed",
  failing: "Commit failing",
  draft: "Draft from Git",
  conflict: "Conflict in Git",
  refused: "Refused from Git",
  deleted: "Deleted in Git",
};

const gitTone = {
  synced: "outline",
  waiting: "neutral",
  failing: "bad",
  draft: "accent",
  conflict: "warn",
  refused: "warn",
  deleted: "neutral",
} as const;

/** What someone has to do about a file, by state. */
const needs: Partial<Record<State, string>> = {
  failing: "rvav can't commit it: check the repository's settings and credentials.",
  draft: "The repository changed it: review the draft, then publish or discard it.",
  conflict: "rvav and the repository both changed it: choose which to keep.",
  refused: "The repository's file can't be imported: fix it there.",
  deleted: "The repository deleted it: rvav keeps the item.",
};

export function GitBadge({ git }: { git: CatalogItemGit }) {
  return <Badge tone={gitTone[git.state]}>{gitStateLabel[git.state]}</Badge>;
}

const short = (sha: string) => sha.slice(0, 8);

function Commit({ c }: { c: NonNullable<CatalogItemGit["last_commit"]> }) {
  const [line] = c.message.split("\n");
  return (
    <span className="text-secondary text-muted">
      <span className="font-mono text-fg">{short(c.sha)}</span> {line} — {c.author}, {relative(c.when)}
    </span>
  );
}

function Problems({ problems, label }: { problems: NonNullable<CatalogItemGit["problems"]>; label: string }) {
  return (
    <ul aria-label={label} className="flex flex-col gap-0.5 text-secondary text-bad">
      {problems.map((p) => (
        <li key={p.field + p.message}>
          {p.field && <span className="font-mono">{p.field}</span>} {p.message}
        </li>
      ))}
    </ul>
  );
}

/** The repository's address without its scheme, as people say it. */
const where = (url: string) => url.replace(/^[a-z+]+:\/\//, "").replace(/\.git$/, "");

/** The catalog's repository in a line above the items: where it is, when it
 * was read, and how many files need someone; its details in a side panel. */
export function GitBanner() {
  const status = useQuery(q.catalogGit());
  const [open, setOpen] = useState(false);
  if (status.isPending) return <Skeleton className="h-11" />;
  if (status.isError) return <ErrorState error={status.error} onRetry={() => void status.refetch()} />;
  const s = status.data;
  if (!s.configured) return null;
  const attention = s.files.filter((f) => needs[f.state]).length;
  return (
    <section
      aria-label="Git repository"
      className={cn(
        "flex flex-wrap items-center gap-x-4 gap-y-1.5 rounded-lg border bg-surface py-2 pr-2 pl-3.5 text-secondary",
        s.error || attention > 0 ? "border-warn/50" : "border-line",
      )}
    >
      <span className="flex min-w-0 items-center gap-2">
        <GitBranch className="size-4 shrink-0 text-muted" aria-hidden />
        <span className="min-w-0 truncate font-mono text-fg" title={s.url}>
          {where(s.url ?? "")}
        </span>
      </span>
      <span className="text-muted">
        <span className="font-mono">{s.branch}</span>, in <span className="font-mono">{s.dir}/</span>
      </span>
      <span className="text-muted">{s.read_at ? `Read ${relative(s.read_at)}` : "Not read yet"}</span>
      {s.error ? (
        <Badge tone="bad">The last read failed</Badge>
      ) : attention > 0 ? (
        <Badge tone="warn">
          {attention} file{attention === 1 ? " needs" : "s need"} you
        </Badge>
      ) : (
        <span className="text-muted">All in step</span>
      )}
      <Button size="sm" variant="quiet" className="ml-auto" onClick={() => setOpen(true)}>
        Details<span className="sr-only"> of the Git repository</span>
      </Button>
      {open && <GitSheet status={s} onClose={() => setOpen(false)} />}
    </section>
  );
}

/** The repository as last read, what needs someone, and a read now. */
function GitSheet({ status: s, onClose }: { status: CatalogGitStatus; onClose: () => void }) {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const read = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/git/read")),
    onSuccess: (next) => {
      qc.setQueryData(keys.catalogAdmin.git, next);
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      if (next.error) toast.error("The repository couldn't be read", { description: next.error });
      else toast.success("Repository read", { description: next.head ? `At ${short(next.head)}` : "It is empty." });
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "The read failed", {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  const latest = useQuery(q.catalogGit()).data ?? s;
  const attention = latest.files.filter((f) => needs[f.state]);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title="Git repository"
      description="rvav commits each item's published version here, and reads the repository's changes back as drafts."
      footer={
        <>
          {can(me, "integrations.manage") && (
            <Button variant="quiet" asChild className="mr-auto">
              <Link to="/admin/integrations" search={{ tab: "git" }}>
                Change or disconnect
              </Link>
            </Button>
          )}
          <Button variant="primary" onClick={() => read.mutate()} disabled={read.isPending}>
            {read.isPending ? "Reading…" : "Read the repository now"}
          </Button>
        </>
      }
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-secondary">
        <dt className="text-muted">Repository</dt>
        <dd className="font-mono break-all">{latest.url}</dd>
        <dt className="text-muted">Branch</dt>
        <dd className="font-mono">{latest.branch}</dd>
        <dt className="text-muted">Directory</dt>
        <dd className="font-mono">{latest.dir}/</dd>
        <dt className="text-muted">Last read</dt>
        <dd>
          {latest.read_at ? relative(latest.read_at) : "Not yet"}
          {latest.head && <span className="font-mono text-muted"> at {short(latest.head)}</span>}
          {latest.read_requested && <span className="text-muted">, and a read is on its way</span>}
        </dd>
      </dl>
      {latest.error && (
        <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary">
          The last read failed: {latest.error}
        </p>
      )}
      <p className="text-secondary text-muted">
        {latest.files.length} file{latest.files.length === 1 ? "" : "s"} in the repository.{" "}
        {attention.length === 0 ? "All in step." : `${attention.length} need${attention.length === 1 ? "s" : ""} you:`}
      </p>
      {attention.length > 0 && (
        <ul
          aria-label="Files that need you"
          className="flex flex-col divide-y divide-line rounded-md border border-line"
        >
          {attention.map((f) => (
            <AttentionRow key={f.path} f={f} />
          ))}
        </ul>
      )}
    </Sheet>
  );
}

function AttentionRow({ f }: { f: CatalogGitFile }) {
  return (
    <li className="flex flex-col gap-1 px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <GitBadge git={f} />
        {f.item ? (
          <Link
            to="/admin/catalog-items/$itemId"
            params={{ itemId: f.item.slug }}
            className="text-body font-medium text-fg hover:text-accent"
          >
            {f.item.name}
          </Link>
        ) : null}
        <span className="font-mono text-caption text-faint">{f.path}</span>
      </div>
      <span className="text-secondary text-muted">{f.error ?? f.note ?? needs[f.state]}</span>
      {f.problems && <Problems problems={f.problems} label={`Problems of ${f.path}`} />}
    </li>
  );
}

function Diff({ lines, label }: { lines: DiffLine[]; label: string }) {
  const sign = { same: " ", added: "+", removed: "-" } as const;
  const tone = { same: "text-muted", added: "bg-ok-soft text-fg", removed: "bg-bad-soft text-fg" } as const;
  return (
    <div
      role="region"
      aria-label={label}
      // Focusable so keyboard users can scroll the diff.
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
      tabIndex={0}
      className="max-h-96 overflow-auto rounded-md border border-line bg-sunken"
    >
      <pre className="py-2 font-mono text-caption leading-5">
        {lines.map((l, i) => (
          <div key={i} className={`px-3 ${tone[l.op]}`}>
            <span aria-hidden>{sign[l.op]} </span>
            <span className="sr-only">{l.op === "same" ? "" : `${l.op}: `}</span>
            {l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}

/** The item's file in the repository. */
export function ItemGitTab({ item }: { item: AdminCatalogItem }) {
  const qc = useQueryClient();
  const git = item.git!;
  const detail = useQuery(q.catalogItemGit(item.id));
  const status = useRef<HTMLDivElement>(null);
  const [taking, setTaking] = useState(false);
  const resolve = useMutation({
    mutationFn: (take: "rvav" | "repository") =>
      call(
        api.POST("/admin/catalog/items/{itemId}/git/resolve", {
          params: { path: { itemId: item.id } },
          body: { take },
        }),
      ),
    // Settled once the page shows it: its buttons stay disabled until then,
    // and focus goes to the file's state.
    onSuccess: async (_, take) => {
      await qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(
        take === "rvav"
          ? "rvav's file will be committed over the repository's change"
          : "The repository's change is a draft",
      );
      status.current?.focus();
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "Not settled", {
        description: isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail) : undefined,
      }),
  });
  const d = detail.data;
  // A conflict with the repository's deletion: nothing of it to take.
  const deletedThere = git.state === "conflict" && d !== undefined && d.repository === undefined;
  const ownDraft = git.state === "draft" && item.draft_version && git.draft_version_id === item.draft_version.id;
  return (
    <Panel title="Git">
      <div className="flex flex-col gap-3">
        <div ref={status} tabIndex={-1} className="flex flex-wrap items-center gap-2 outline-none">
          <GitBadge git={git} />
          <span className="font-mono text-secondary text-muted">{git.path}</span>
        </div>
        {git.last_commit && (
          <p className="flex flex-wrap gap-1 text-secondary">
            <span className="text-muted">Last commit:</span> <Commit c={git.last_commit} />
          </p>
        )}
        {git.state === "failing" && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-secondary">
            Committing failed {git.attempts} time{git.attempts === 1 ? "" : "s"}: {git.error}.
            {git.next_attempt_at && <> Tried again {relative(git.next_attempt_at)}.</>}
          </p>
        )}
        {git.state === "waiting" && (
          <p className="text-secondary text-muted">
            rvav's change will be committed at the next read of the repository.
          </p>
        )}
        {git.note && <p className="text-secondary text-muted">{git.note}</p>}
        {git.problems && <Problems problems={git.problems} label="Why the repository's file was refused" />}
        {ownDraft && item.draft_version && (
          <p className="text-secondary">
            The repository's change is draft {item.draft_version.number}: review it in Versions, then publish it (rvav
            commits it back) or discard it (rvav's file is committed again).
          </p>
        )}
        {git.state === "conflict" && (
          <div className="flex flex-col gap-2">
            <p className="text-secondary">
              {deletedThere
                ? "The repository deleted the file: keep rvav's to add it back."
                : "Keep rvav's file, committed over the repository's change; or take the repository's, as a draft (an open draft in rvav is discarded)."}
            </p>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="primary" disabled={resolve.isPending} onClick={() => resolve.mutate("rvav")}>
                Keep rvav's
              </Button>
              <Button
                size="sm"
                disabled={resolve.isPending || deletedThere}
                onClick={() => (item.draft_version ? setTaking(true) : resolve.mutate("repository"))}
              >
                Take the repository's
              </Button>
            </div>
          </div>
        )}
        {taking && item.draft_version && (
          <ConfirmDialog
            open
            onOpenChange={(o) => !o && setTaking(false)}
            title={`Discard draft ${item.draft_version.number}?`}
            description="The repository's change becomes the item's draft, in place of rvav's."
            consequences={`What draft ${item.draft_version.number} changed is lost. The published version stays as it is.`}
            confirmLabel="Take the repository's"
            onConfirm={() => {
              // Closed before it runs: once settled, the focus goes to the
              // file's state, not back to a button that is gone.
              setTaking(false);
              resolve.mutate("repository");
            }}
          />
        )}
        {detail.isError && <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />}
        {d && d.diff.some((l) => l.op !== "same") && (
          <div className="flex flex-col gap-1">
            <h3 className="text-secondary font-semibold">
              {git.state === "draft"
                ? "From rvav's file to the repository's draft"
                : "From rvav's file to the repository's"}
            </h3>
            <Diff lines={d.diff} label="Changes from rvav's file to the repository's" />
          </div>
        )}
      </div>
    </Panel>
  );
}
