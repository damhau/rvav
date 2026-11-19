// Policies (S9, ADR-0020): approval, day-2, lease and naming policies, lease rules (ADR-0048),
// deployment limits (ADR-0047), and what applies for a project, environment, item or action. Conditions are CEL,
// checked by the server on sample variables when saved.
import { useNavigate } from "@tanstack/react-router";
import { Page } from "@/components/common/page";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { usePolicyRights } from "./policies/shared";
import { ApprovalPolicies } from "./policies/ApprovalPolicies";
import { Day2Policies } from "./policies/Day2Policies";
import { DeploymentLimits } from "./policies/DeploymentLimits";
import { LeasePolicies } from "./policies/LeasePolicies";
import { LeaseRules } from "./policies/LeaseRules";
import { NamingPolicies } from "./policies/NamingPolicies";
import { RvavQuotas } from "./policies/RvavQuotas";
import { WhatApplies } from "./policies/WhatApplies";

export const policiesTabs = ["approval", "day2", "lease", "limits", "naming", "quotas", "explain"] as const;
export type PoliciesTab = (typeof policiesTabs)[number];

export function PoliciesPage({ tab = "approval" }: { tab?: PoliciesTab }) {
  const navigate = useNavigate();
  const { me } = useCurrentProject();
  const rights = usePolicyRights();
  // Project admins write their projects' own policies; naming policies stay the administrators'.
  const naming = can(me, "policies.read");
  // rvav's quota is for all of it: those who read every project's settings (ADR-0047).
  const quotas = me.permissions.includes("project.settings.read");
  const shown = (tab === "naming" && !naming) || (tab === "quotas" && !quotas) ? "approval" : tab;
  return (
    <Page>
      <PageHeader
        title="Policies"
        description={
          naming
            ? "Who approves what, which day-2 actions are allowed, how long deployments live, how many and how large they may be, and how VMs are named."
            : "Who approves what, which day-2 actions are allowed, how long deployments live and how many and how large they may be in your projects. You read the global policies and your projects', and write your projects' own unless an administrator set them."
        }
      />
      <Tabs
        value={shown}
        onValueChange={(next) =>
          void navigate({ to: "/admin/policies", search: { tab: next as PoliciesTab }, replace: true })
        }
      >
        <TabsList aria-label="Kinds of policy">
          <TabsTrigger value="approval">Approval</TabsTrigger>
          <TabsTrigger value="day2">Day-2</TabsTrigger>
          <TabsTrigger value="lease">Lease</TabsTrigger>
          <TabsTrigger value="limits">Limits</TabsTrigger>
          {naming && <TabsTrigger value="naming">Naming</TabsTrigger>}
          {quotas && <TabsTrigger value="quotas">Quotas</TabsTrigger>}
          <TabsTrigger value="explain">What applies here</TabsTrigger>
        </TabsList>
        <TabsContent value="approval" className="pt-4">
          <ApprovalPolicies rights={rights} />
        </TabsContent>
        <TabsContent value="day2" className="pt-4">
          <Day2Policies rights={rights} />
        </TabsContent>
        <TabsContent value="lease" className="flex flex-col gap-4 pt-4">
          <LeasePolicies rights={rights} />
          <LeaseRules rights={rights} />
        </TabsContent>
        <TabsContent value="limits" className="pt-4">
          <DeploymentLimits rights={rights} />
        </TabsContent>
        {naming && (
          <TabsContent value="naming" className="pt-4">
            <NamingPolicies canEdit={rights.global} />
          </TabsContent>
        )}
        {quotas && (
          <TabsContent value="quotas" className="pt-4">
            <RvavQuotas canEdit={can(me, "project.quotas.manage")} />
          </TabsContent>
        )}
        <TabsContent value="explain" className="pt-4">
          <WhatApplies />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
