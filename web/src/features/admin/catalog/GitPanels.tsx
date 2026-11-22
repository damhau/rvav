// The catalog's Git repository (C1.4, ADR-0029): its state on the catalog
// page, with the files that need someone; and each item's file on its
// page: its last commit, the repository's change, a conflict to settle.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { GitBranch } from "lucide-react";
import { useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { cn } from "@/lib/utils";
import { relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type CatalogGitStatus = components["schemas"]["CatalogGitStatus"];

type State = CatalogItemGit["state"];

const tr = i18n.getFixedT(null, "admin-catalog");

export const gitStateLabel = (state: State) => tr(`git.state.${state}`);

const gitTone = {
  synced: "outline",
  waiting: "neutral",
  failing: "bad",
  draft: "accent",
  conflict: "warn",
  refused: "warn",
  deleted: "neutral",
} as const;

const needing = ["failing", "draft", "conflict", "refused", "deleted"] as const;

/** What someone has to do about a file, by state. */
function needs(state: State): string | undefined {
  const s = needing.find((n) => n === state);
  return s && tr(`git.needs.${s}`);
}

export function GitBadge({ git }: { git: CatalogItemGit }) {
  useTranslation();
  return <Badge tone={gitTone[git.state]}>{gitStateLabel(git.state)}</Badge>;
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
  const { t } = useTranslation("admin-catalog");
  const status = useQuery(q.catalogGit());
  const [open, setOpen] = useState(false);
  if (status.isPending) return <Skeleton className="h-11" />;
  if (status.isError) return <ErrorState error={status.error} onRetry={() => void status.refetch()} />;
  const s = status.data;
  if (!s.configured) return null;
  const attention = s.files.filter((f) => needs(f.state)).length;
  return (
    <section
      aria-label={t("git.repository")}
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
        <Trans
          t={t}
          i18nKey="git.branchIn"
          values={{ branch: s.branch, dir: s.dir }}
          components={{ branch: <span className="font-mono" />, dir: <span className="font-mono" /> }}
        />
      </span>
      <span className="text-muted">{s.read_at ? t("git.read", { when: relative(s.read_at) }) : t("git.notRead")}</span>
      {s.error ? (
        <Badge tone="bad">{t("git.lastReadFailed")}</Badge>
      ) : attention > 0 ? (
        <Badge tone="warn">{t("git.filesNeedYou", { count: attention })}</Badge>
      ) : (
        <span className="text-muted">{t("git.allInStep")}</span>
      )}
      <Button size="sm" variant="quiet" className="ml-auto" onClick={() => setOpen(true)}>
        <Trans t={t} i18nKey="git.details" components={{ sr: <span className="sr-only" /> }} />
      </Button>
      {open && <GitSheet status={s} onClose={() => setOpen(false)} />}
    </section>
  );
}

/** The repository as last read, what needs someone, and a read now. */
function GitSheet({ status: s, onClose }: { status: CatalogGitStatus; onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const read = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/git/read")),
    onSuccess: (next) => {
      qc.setQueryData(keys.catalogAdmin.git, next);
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      if (next.error) toast.error(t("git.notReadToast"), { description: next.error });
      else
        toast.success(t("git.readToast"), {
          description: next.head ? t("git.at", { sha: short(next.head) }) : t("git.empty"),
        });
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : t("git.readFailed"), {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  const latest = useQuery(q.catalogGit()).data ?? s;
  const attention = latest.files.filter((f) => needs(f.state));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("git.repository")}
      description={t("git.sheetDescription")}
      footer={
        <>
          {can(me, "integrations.manage") && (
            <Button variant="quiet" asChild className="mr-auto">
              <Link to="/admin/integrations/$kind" params={{ kind: "git" }}>
                {t("git.change")}
              </Link>
            </Button>
          )}
          <Button variant="primary" onClick={() => read.mutate()} disabled={read.isPending}>
            {read.isPending ? t("git.reading") : t("git.readNow")}
          </Button>
        </>
      }
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-secondary">
        <dt className="text-muted">{t("git.repositoryTerm")}</dt>
        <dd className="font-mono break-all">{latest.url}</dd>
        <dt className="text-muted">{t("git.branch")}</dt>
        <dd className="font-mono">{latest.branch}</dd>
        <dt className="text-muted">{t("git.directory")}</dt>
        <dd className="font-mono">{latest.dir}/</dd>
        <dt className="text-muted">{t("git.lastRead")}</dt>
        <dd>
          {latest.read_at ? relative(latest.read_at) : t("git.notYet")}
          {latest.head && <span className="font-mono text-muted">{t("git.atSha", { sha: short(latest.head) })}</span>}
          {latest.read_requested && <span className="text-muted">{t("git.readComing")}</span>}
        </dd>
      </dl>
      {latest.error && (
        <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary">
          {t("git.lastReadFailedWith", { error: latest.error })}
        </p>
      )}
      <p className="text-secondary text-muted">
        {t("git.files", { count: latest.files.length })}{" "}
        {attention.length === 0 ? t("git.allInStepDot") : t("git.needYou", { count: attention.length })}
      </p>
      {attention.length > 0 && (
        <ul
          aria-label={t("git.filesThatNeedYou")}
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
  const { t } = useTranslation("admin-catalog");
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
      <span className="text-secondary text-muted">{f.error ?? f.note ?? needs(f.state)}</span>
      {f.problems && <Problems problems={f.problems} label={t("git.problemsOf", { path: f.path })} />}
    </li>
  );
}

function Diff({ lines, label }: { lines: DiffLine[]; label: string }) {
  const { t } = useTranslation("admin-catalog");
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
            <span className="sr-only">{l.op === "same" ? "" : t(`git.diffOp.${l.op}`)}</span>
            {l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}

/** The item's file in the repository. */
export function ItemGitTab({ item }: { item: AdminCatalogItem }) {
  const { t } = useTranslation("admin-catalog");
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
      toast.success(take === "rvav" ? t("git.committed") : t("git.takenDraft"));
      status.current?.focus();
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : t("git.notSettled"), {
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
            <span className="text-muted">{t("git.lastCommit")}</span> <Commit c={git.last_commit} />
          </p>
        )}
        {git.state === "failing" && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-secondary">
            {t("git.failingTimes", { count: git.attempts, error: git.error })}
            {git.next_attempt_at && t("git.triedAgain", { when: relative(git.next_attempt_at) })}
          </p>
        )}
        {git.state === "waiting" && <p className="text-secondary text-muted">{t("git.waiting")}</p>}
        {git.note && <p className="text-secondary text-muted">{git.note}</p>}
        {git.problems && <Problems problems={git.problems} label={t("git.refusedWhy")} />}
        {ownDraft && item.draft_version && (
          <p className="text-secondary">{t("git.ownDraft", { number: item.draft_version.number })}</p>
        )}
        {git.state === "conflict" && (
          <div className="flex flex-col gap-2">
            <p className="text-secondary">{deletedThere ? t("git.deletedThere") : t("git.conflict")}</p>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="primary" disabled={resolve.isPending} onClick={() => resolve.mutate("rvav")}>
                {t("git.keepRvav")}
              </Button>
              <Button
                size="sm"
                disabled={resolve.isPending || deletedThere}
                onClick={() => (item.draft_version ? setTaking(true) : resolve.mutate("repository"))}
              >
                {t("git.takeRepository")}
              </Button>
            </div>
          </div>
        )}
        {taking && item.draft_version && (
          <ConfirmDialog
            open
            onOpenChange={(o) => !o && setTaking(false)}
            title={t("git.discardTitle", { number: item.draft_version.number })}
            description={t("git.discardBody")}
            consequences={t("git.discardConsequences", { number: item.draft_version.number })}
            confirmLabel={t("git.takeRepository")}
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
              {git.state === "draft" ? t("git.fromToDraft") : t("git.fromTo")}
            </h3>
            <Diff lines={d.diff} label={t("git.changes")} />
          </div>
        )}
      </div>
    </Panel>
  );
}
