// The setup checklist (U1.5, ADR-0030): the steps that set rvav up from its
// UI, each linking to its page and ticked once its page has what it needs.
// An install without seed data or integration settings opens on it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Circle, CircleCheck } from "lucide-react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { CHANGE_PASSWORD_PATH } from "@/lib/auth";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { cn } from "@/lib/utils";
import { failed } from "./governance/common";

type S = components["schemas"];
type Key = S["SetupStep"]["key"];

type Target =
  | { to: "/admin/integrations"; tab: string; label: string }
  | { to: "/admin/projects" | "/admin/catalog-items"; label: string }
  | { href: string; label: string };

const steps: Record<Key, { title: string; what: string; target: Target }> = {
  password: {
    title: "Change the administrator's password",
    what: "The install's password is known to whoever installed rvav. Signed in as admin, you choose your own.",
    target: { href: CHANGE_PASSWORD_PATH, label: "Change password" },
  },
  provider: {
    title: "Set the identity provider",
    what: "Where users sign in: Entra ID, Keycloak, Okta or another OpenID Connect provider. Test it with a trial sign-in before saving.",
    target: { to: "/admin/integrations", tab: "oidc", label: "Identity provider" },
  },
  admin_group: {
    title: "Map a group to admin",
    what: "Its members administer rvav once they sign in with the provider, so the local account is kept for when the provider is down.",
    target: { to: "/admin/projects", label: "OIDC group mappings" },
  },
  vcenter: {
    title: "Connect vCenter",
    what: "Where VMs are built, with rvav's service account.",
    target: { to: "/admin/integrations", tab: "vcenter", label: "vCenter" },
  },
  projects: {
    title: "Add environments and projects",
    what: "The environments VMs deploy to (development, production, …), the projects that request in them, with their members or groups, and a placement rule for each: where its VMs go.",
    target: { to: "/admin/projects", label: "Projects" },
  },
  ipam: {
    title: "Connect EfficientIP",
    what: "Each VM's address and DNS records.",
    target: { to: "/admin/integrations", tab: "efficientip", label: "EfficientIP" },
  },
  awx: {
    title: "Connect AWX",
    what: "The jobs that configure VMs once built, and day-2 actions.",
    target: { to: "/admin/integrations", tab: "awx", label: "AWX" },
  },
  catalog_item: {
    title: "Publish a first catalog item",
    what: "What users request: a VM blueprint, an AWX job or both. Then the templates, networks and placement it needs, in Templates and networks.",
    target: { to: "/admin/catalog-items", label: "Catalog items" },
  },
};

function Go({ target, disabled }: { target: Target; disabled?: string }) {
  if (disabled) return <span className="text-xs text-faint">{disabled}</span>;
  return (
    <Button size="sm" asChild>
      {"href" in target ? (
        <a href={target.href}>{target.label}</a>
      ) : target.to === "/admin/integrations" ? (
        <Link to="/admin/integrations" search={{ tab: target.tab }}>
          {target.label}
        </Link>
      ) : (
        <Link to={target.to}>{target.label}</Link>
      )}
    </Button>
  );
}

export function SetupPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  // Read again each time it opens: its steps are done on other pages.
  const setup = useQuery({ ...adm.setup(), refetchOnMount: "always" });
  const hide = useMutation({
    mutationFn: (hidden: boolean) => call(api.PUT("/admin/setup", { body: { hidden } })),
    onSuccess: (s) => {
      qc.setQueryData(adm.setup().queryKey, s);
      toast.success(s.hidden ? "The checklist won't open at sign-in" : "The checklist opens at sign-in again", {
        description: s.hidden ? "It stays here, in Administration › Setup." : undefined,
      });
    },
    onError: (e) => failed(e, "Not changed"),
  });
  const done = setup.data?.steps.filter((s) => s.done).length ?? 0;
  const total = setup.data?.steps.length ?? 8;
  return (
    <Page>
      <PageHeader
        title="Set up rvav"
        meta="The steps that get a first request through. Each opens its page, and is ticked once that page has what it needs."
        actions={
          setup.data &&
          !setup.data.complete && (
            <Button size="sm" variant="ghost" disabled={hide.isPending} onClick={() => hide.mutate(!setup.data.hidden)}>
              {setup.data.hidden ? "Open it at sign-in again" : "Don't open it at sign-in"}
            </Button>
          )
        }
      />
      <Panel
        title="Setup checklist"
        flush
        actions={
          setup.data && (
            <span className="flex items-center gap-2 text-xs text-muted">
              <span
                role="progressbar"
                aria-label="Steps done"
                aria-valuemin={0}
                aria-valuemax={total}
                aria-valuenow={done}
                className="h-1.5 w-24 overflow-hidden rounded-full bg-sunken"
              >
                <span className="block h-full bg-ok" style={{ width: `${(100 * done) / total}%` }} />
              </span>
              {done} of {total} done
            </span>
          )
        }
      >
        {setup.isPending && <SkeletonRows rows={8} cols={2} />}
        {setup.isError && <ErrorState error={setup.error} onRetry={() => void setup.refetch()} />}
        {setup.data?.complete && (
          <p role="status" className="border-b border-line px-4 py-3 text-[13px]">
            Every step is done: users can sign in and request. The checklist no longer opens at sign-in.
          </p>
        )}
        {setup.data && (
          <ol aria-label="Steps" className="flex flex-col">
            {setup.data.steps.map((s, i) => {
              const step = steps[s.key];
              const notLocal =
                s.key === "password" && !s.done && !me.local ? "Sign in as admin to change it." : undefined;
              return (
                <li
                  key={s.key}
                  aria-label={`${step.title}: ${s.done ? "done" : "to do"}`}
                  className="flex items-start gap-3 border-b border-line px-4 py-3 last:border-b-0"
                >
                  {s.done ? (
                    <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />
                  ) : (
                    <Circle className="mt-0.5 size-4 shrink-0 text-faint" aria-hidden />
                  )}
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <p className={cn("text-[13px] font-medium", s.done && "text-muted")}>
                      <span className="mr-1.5 font-mono text-xs text-faint">{i + 1}.</span>
                      {step.title}
                    </p>
                    <p className="text-xs text-muted">{step.what}</p>
                    <p className="text-xs break-words">
                      <span className="text-faint">{s.done ? "Done: " : "Now: "}</span>
                      {s.detail}
                    </p>
                  </div>
                  <Go target={step.target} disabled={notLocal} />
                </li>
              );
            })}
          </ol>
        )}
      </Panel>
    </Page>
  );
}
