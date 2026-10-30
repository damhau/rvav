// The catalog's Git repository (C1.4, ADR-0029): its state on the catalog
// page, with the files that need someone; and each item's file on its
// page: its last commit, the repository's change, a conflict to settle.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogGitFile, CatalogItemGit, DiffLine } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { relative } from "@/lib/utils";

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
    <span className="text-xs text-muted">
      <span className="font-mono text-fg">{short(c.sha)}</span> {line} — {c.author}, {relative(c.when)}
    </span>
  );
}

function Problems({ problems, label }: { problems: NonNullable<CatalogItemGit["problems"]>; label: string }) {
  return (
    <ul aria-label={label} className="flex flex-col gap-0.5 text-xs text-bad">
      {problems.map((p) => (
        <li key={p.field + p.message}>
          {p.field && <span className="font-mono">{p.field}</span>} {p.message}
        </li>
      ))}
    </ul>
  );
}

/** The catalog's repository: as last read, what needs someone, and a
 * read now. */
export function GitRepositoryPanel() {
  const qc = useQueryClient();
  const status = useQuery(q.catalogGit());
  const read = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/git/read")),
    onSuccess: (s) => {
      qc.setQueryData(keys.catalogAdmin.git, s);
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      if (s.error) toast.error("The repository couldn't be read", { description: s.error });
      else toast.success("Repository read", { description: s.head ? `At ${short(s.head)}` : "It is empty." });
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "The read failed", {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  if (status.isPending) return <Skeleton className="h-28" />;
  if (status.isError) return <ErrorState error={status.error} onRetry={() => void status.refetch()} />;
  const s = status.data;
  if (!s.configured) return null;
  const attention = s.files.filter((f) => needs[f.state]);
  return (
    <Panel
      title="Git repository"
      actions={
        <Button size="sm" onClick={() => read.mutate()} disabled={read.isPending}>
          {read.isPending ? "Reading…" : "Read the repository now"}
        </Button>
      }
    >
      <div className="flex flex-col gap-3">
        <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-[auto_1fr]">
          <dt className="text-muted">Repository</dt>
          <dd className="font-mono break-all">{s.url}</dd>
          <dt className="text-muted">Branch and directory</dt>
          <dd className="font-mono">
            {s.branch} · {s.dir}/
          </dd>
          <dt className="text-muted">Last read</dt>
          <dd>
            {s.read_at ? relative(s.read_at) : "Not yet"}
            {s.head && <span className="font-mono text-muted"> at {short(s.head)}</span>}
            {s.read_requested && <span className="text-muted"> · a read is on its way</span>}
          </dd>
        </dl>
        {s.error && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-xs">
            The last read failed: {s.error}
          </p>
        )}
        <p className="text-xs text-muted">
          {s.files.length} file{s.files.length === 1 ? "" : "s"} in the repository.{" "}
          {attention.length === 0
            ? "All in step."
            : `${attention.length} need${attention.length === 1 ? "s" : ""} you:`}
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
      </div>
    </Panel>
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
            className="text-[13px] font-medium text-fg hover:text-accent"
          >
            {f.item.name}
          </Link>
        ) : null}
        <span className="font-mono text-[11px] text-faint">{f.path}</span>
      </div>
      <span className="text-xs text-muted">{f.error ?? f.note ?? needs[f.state]}</span>
      {f.problems && <Problems problems={f.problems} label={`Problems of ${f.path}`} />}
    </li>
  );
}

function Diff({ lines, label }: { lines: DiffLine[]; label: string }) {
  const sign = { same: " ", added: "+", removed: "-" } as const;
  const tone = { same: "text-muted", added: "bg-ok-soft text-fg", removed: "bg-bad-soft text-fg" } as const;
  return (
    <pre
      aria-label={label}
      // Focusable so keyboard users can scroll the diff.
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
      tabIndex={0}
      className="max-h-96 overflow-auto rounded-md border border-line bg-sunken py-2 font-mono text-[11px] leading-5"
    >
      {lines.map((l, i) => (
        <div key={i} className={`px-3 ${tone[l.op]}`}>
          <span aria-hidden>{sign[l.op]} </span>
          <span className="sr-only">{l.op === "same" ? "" : `${l.op}: `}</span>
          {l.text}
        </div>
      ))}
    </pre>
  );
}

/** The item's file in the repository. */
export function ItemGitTab({ item }: { item: AdminCatalogItem }) {
  const qc = useQueryClient();
  const git = item.git!;
  const detail = useQuery(q.catalogItemGit(item.id));
  const resolve = useMutation({
    mutationFn: (take: "rvav" | "repository") =>
      call(
        api.POST("/admin/catalog/items/{itemId}/git/resolve", {
          params: { path: { itemId: item.id } },
          body: { take },
        }),
      ),
    onSuccess: (it, take) => {
      qc.setQueryData(keys.catalogAdmin.item(item.id), it);
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(
        take === "rvav"
          ? "rvav's file will be committed over the repository's change"
          : "The repository's change is a draft",
      );
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "Not settled", {
        description: isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail) : undefined,
      }),
  });
  const d = detail.data;
  return (
    <Panel title="Git">
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <GitBadge git={git} />
          <span className="font-mono text-xs text-muted">{git.path}</span>
        </div>
        {git.last_commit && (
          <p className="flex flex-wrap gap-1 text-xs">
            <span className="text-muted">Last commit:</span> <Commit c={git.last_commit} />
          </p>
        )}
        {git.state === "failing" && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-xs">
            Committing failed {git.attempts} time{git.attempts === 1 ? "" : "s"}: {git.error}.
            {git.next_attempt_at && <> Tried again {relative(git.next_attempt_at)}.</>}
          </p>
        )}
        {git.state === "waiting" && (
          <p className="text-xs text-muted">rvav's change will be committed at the next read of the repository.</p>
        )}
        {git.note && <p className="text-xs text-muted">{git.note}</p>}
        {git.problems && <Problems problems={git.problems} label="Why the repository's file was refused" />}
        {git.state === "draft" && item.draft_version && (
          <p className="text-xs">
            The repository's change is draft {item.draft_version.number}: review it in Versions, then publish it (rvav
            commits it back) or discard it (rvav's file is committed again).
          </p>
        )}
        {git.state === "conflict" && (
          <div className="flex flex-col gap-2">
            <p className="text-xs">
              Keep rvav's file, committed over the repository's change; or take the repository's, as a draft (an open
              draft in rvav is discarded).
            </p>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="primary" disabled={resolve.isPending} onClick={() => resolve.mutate("rvav")}>
                Keep rvav's
              </Button>
              <Button
                size="sm"
                disabled={resolve.isPending}
                onClick={() => {
                  if (
                    !item.draft_version ||
                    window.confirm(`Discard draft ${item.draft_version.number} and take the repository's change?`)
                  )
                    resolve.mutate("repository");
                }}
              >
                Take the repository's
              </Button>
            </div>
          </div>
        )}
        {detail.isError && <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />}
        {d && d.diff.length > 0 && (
          <div className="flex flex-col gap-1">
            <h3 className="text-xs font-semibold">
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
