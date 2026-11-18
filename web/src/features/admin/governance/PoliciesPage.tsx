// Policies (S9, ADR-0020): approval, day-2, lease and naming policies, and
// what applies for a project, environment, item or action. Conditions are CEL,
// checked by the server on sample variables when saved.
import { useNavigate } from "@tanstack/react-router";
import { Page } from "@/components/common/page";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { ApprovalPolicies } from "./policies/ApprovalPolicies";
import { Day2Policies } from "./policies/Day2Policies";
import { LeasePolicies } from "./policies/LeasePolicies";
import { NamingPolicies } from "./policies/NamingPolicies";
import { WhatApplies } from "./policies/WhatApplies";

export const policiesTabs = ["approval", "day2", "lease", "naming", "explain"] as const;
export type PoliciesTab = (typeof policiesTabs)[number];

export function PoliciesPage({ tab = "approval" }: { tab?: PoliciesTab }) {
  const navigate = useNavigate();
  const { me } = useCurrentProject();
  const canEdit = can(me, "policies.manage");
  return (
    <Page>
      <PageHeader
        title="Policies"
        description="Who approves what, which day-2 actions are allowed, how long deployments live and how VMs are named."
      />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({ to: "/admin/policies", search: { tab: next as PoliciesTab }, replace: true })
        }
      >
        <TabsList aria-label="Kinds of policy">
          <TabsTrigger value="approval">Approval</TabsTrigger>
          <TabsTrigger value="day2">Day-2</TabsTrigger>
          <TabsTrigger value="lease">Lease</TabsTrigger>
          <TabsTrigger value="naming">Naming</TabsTrigger>
          <TabsTrigger value="explain">What applies here</TabsTrigger>
        </TabsList>
        <TabsContent value="approval" className="pt-4">
          <ApprovalPolicies canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="day2" className="pt-4">
          <Day2Policies canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="lease" className="pt-4">
          <LeasePolicies canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="naming" className="pt-4">
          <NamingPolicies canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="explain" className="pt-4">
          <WhatApplies />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
