// The setup checklist (U1.5, ADR-0030): the steps that set rvav up from its
// UI, each linking to its page and ticked once its page has what it needs.
// An install without seed data or integration settings opens on it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Button } from "@/components/ui/button";
import { StepMark, type StepState, stepNow } from "@/components/ui/step-mark";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { CHANGE_PASSWORD_PATH } from "@/lib/auth";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { cn } from "@/lib/utils";
import { failed } from "./governance/common";
import { serverText } from "@/lib/i18n/names";

type S = components["schemas"];
type Key = S["SetupStep"]["key"];

type Target =
  | { to: "/admin/integrations/$kind"; kind: string }
  | { to: "/admin/projects"; tab: "projects" | "groups" }
  | { to: "/admin/catalog-items" }
  | { href: string };

/** A step's detail as the server wrote it (internal/core/setup.go), in the language shown; addresses and group names as they are. */
export function setupDetail(t: TFunction<"admin">, detail: string): string {
  const fake = /^The development fake \((RVAV_FAKE_[A-Z0-9_]+)\)\.$/.exec(detail);
  if (fake) return t("setup.detail.fake", { variable: fake[1] });
  const projects = /^(\d+) environments?, (\d+) projects?, (\d+) placement rules?$/.exec(detail);
  if (projects)
    return [
      t("setup.detail.environments", { count: Number(projects[1]) }),
      t("setup.detail.projects", { count: Number(projects[2]) }),
      t("setup.detail.placements", { count: Number(projects[3]) }),
    ].join(", ");
  const items = /^(\d+) items?, (\d+) published$/.exec(detail);
  if (items)
    return [
      t("setup.detail.items", { count: Number(items[1]) }),
      t("setup.detail.published", { count: Number(items[2]) }),
    ].join(", ");
  return serverText("admin", "setup.detail.fixed", detail);
}

const targets: Record<Key, Target> = {
  password: { href: CHANGE_PASSWORD_PATH },
  provider: { to: "/admin/integrations/$kind", kind: "oidc" },
  admin_group: { to: "/admin/projects", tab: "groups" },
  vcenter: { to: "/admin/integrations/$kind", kind: "vcenter" },
  projects: { to: "/admin/projects", tab: "projects" },
  ipam: { to: "/admin/integrations/$kind", kind: "efficientip" },
  awx: { to: "/admin/integrations/$kind", kind: "awx" },
  catalog_item: { to: "/admin/catalog-items" },
};

function Go({ step, disabled, state }: { step: Key; disabled?: string; state: StepState }) {
  const { t } = useTranslation("admin");
  const target = targets[step];
  const label = t(`setup.step.${step}.go`);
  if (disabled) return <span className="text-secondary text-faint">{disabled}</span>;
  return (
    <Button size="sm" variant={state === "now" ? "primary" : state === "done" ? "quiet" : "secondary"} asChild>
      {"href" in target ? (
        <a href={target.href}>{label}</a>
      ) : target.to === "/admin/integrations/$kind" ? (
        <Link to="/admin/integrations/$kind" params={{ kind: target.kind }}>
          {label}
        </Link>
      ) : target.to === "/admin/projects" ? (
        <Link to="/admin/projects" search={{ tab: target.tab }}>
          {label}
        </Link>
      ) : (
        <Link to={target.to}>{label}</Link>
      )}
    </Button>
  );
}

export function SetupPage() {
  const { t } = useTranslation(["admin", "shell"]);
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  // Read again each time it opens: its steps are done on other pages.
  const setup = useQuery({ ...adm.setup(), refetchOnMount: "always" });
  const hide = useMutation({
    mutationFn: (hidden: boolean) => call(api.PUT("/admin/setup", { body: { hidden } })),
    onSuccess: (s) => {
      qc.setQueryData(adm.setup().queryKey, s);
      toast.success(s.hidden ? t("setup.hidden") : t("setup.shown"), {
        description: s.hidden ? t("setup.hiddenBody") : undefined,
      });
    },
    onError: (e) => failed(e, t("setup.notChanged")),
  });
  const done = setup.data?.steps.filter((s) => s.done).length ?? 0;
  const total = setup.data?.steps.length ?? 8;
  const blocked = (s: S["SetupStep"]) =>
    s.key === "password" && !s.done && !me.local ? t("setup.localOnly") : undefined;
  // The first step this administrator can do from here.
  const now = setup.data?.steps.find((s) => !s.done && !blocked(s))?.key;
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.setup")}
        description={t("setup.description")}
        actions={
          setup.data &&
          !setup.data.complete && (
            <Button variant="quiet" disabled={hide.isPending} onClick={() => hide.mutate(!setup.data.hidden)}>
              {setup.data.hidden ? t("setup.openAgain") : t("setup.dontOpen")}
            </Button>
          )
        }
      />
      <Panel
        title={t("setup.checklist")}
        flush
        actions={
          setup.data && (
            <span className="flex items-center gap-2 text-secondary text-muted">
              <span
                role="progressbar"
                aria-label={t("setup.stepsDone")}
                aria-valuemin={0}
                aria-valuemax={total}
                aria-valuenow={done}
                className="h-1.5 w-24 overflow-hidden rounded-full bg-sunken"
              >
                <span className="block h-full bg-ok" style={{ width: `${(100 * done) / total}%` }} />
              </span>
              {t("setup.progress", { done, total })}
            </span>
          )
        }
      >
        {setup.isPending && <SkeletonRows rows={8} cols={2} />}
        {setup.isError && <ErrorState error={setup.error} onRetry={() => void setup.refetch()} />}
        {setup.data?.complete && (
          <p role="status" className="border-b border-line px-4 py-3 text-body">
            {t("setup.complete")}
          </p>
        )}
        {setup.data && (
          <ol aria-label={t("setup.steps")} className="flex flex-col">
            {setup.data.steps.map((s, i) => {
              const title = t(`setup.step.${s.key}.title`);
              const state: StepState = s.done ? "done" : s.key === now ? "now" : "later";
              return (
                <li
                  key={s.key}
                  aria-label={s.done ? t("setup.stepDone", { title }) : t("setup.stepToDo", { title })}
                  aria-current={state === "now" ? "step" : undefined}
                  className={cn(
                    "flex flex-wrap items-start gap-x-3 gap-y-2 border-b border-line px-4 py-3 last:border-b-0 sm:flex-nowrap",
                    state === "now" && stepNow,
                  )}
                >
                  <StepMark n={i + 1} state={state} className="-mt-px" />
                  <div className="flex min-w-0 flex-[1_1_calc(100%-3rem)] flex-col gap-0.5 sm:flex-1">
                    <p className={cn("text-body font-medium", s.done && "text-muted")}>{title}</p>
                    <p className="text-secondary text-muted">{t(`setup.step.${s.key}.what`)}</p>
                    <p className="text-secondary break-words">
                      <span className="text-faint">{s.done ? t("setup.done") : t("setup.now")}</span>
                      {setupDetail(t, s.detail)}
                    </p>
                  </div>
                  <div className="pl-9 sm:pl-0">
                    <Go step={s.key} disabled={blocked(s)} state={state} />
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </Panel>
    </Page>
  );
}
