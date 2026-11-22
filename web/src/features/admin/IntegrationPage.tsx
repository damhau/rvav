import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plug } from "lucide-react";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { adm } from "@/lib/query";
import { IdentityProvider } from "./IdentityProvider";
import { Integration, integrationAbout, integrationKinds, integrationTitle } from "./IntegrationSettings";
import { InventoryToken } from "./InventoryToken";
import { NSXManagers } from "./NSXManagers";
import { ServiceNowCmdb, ServiceNowToken } from "./ServiceNow";
import { VCenters } from "./VCenters";

type Kind = (typeof integrationKinds)[number];

const isKind = (k: string): k is Kind => (integrationKinds as readonly string[]).includes(k) && k !== "console";

/** One integration's page: its settings, test and what goes with it (the console and other vCenters, AWX's inventory token, other NSX Managers, ServiceNow's token and CMDB). */
export function IntegrationPage({ kind }: { kind: string }) {
  const list = useQuery(adm.integrations());
  const known = isKind(kind);
  const it = known ? list.data?.items.find((i) => i.kind === kind) : undefined;
  const console = kind === "vcenter" ? list.data?.items.find((i) => i.kind === "console") : undefined;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/integrations">Integrations</Link>
          </Breadcrumbs>
        }
        title={known ? integrationTitle(kind) : "Integration not found"}
        description={known ? integrationAbout(kind) : undefined}
      />
      {list.isPending && <Skeleton className="h-64" />}
      {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      {list.isSuccess && !it && (
        <EmptyState
          icon={Plug}
          title="No such integration"
          body={`rvav doesn't connect to anything called ${kind}.`}
          action={
            <Button asChild>
              <Link to="/admin/integrations">All integrations</Link>
            </Button>
          }
        />
      )}
      {it &&
        (it.kind === "oidc" ? (
          <IdentityProvider key={it.updated_at ?? "unsaved"} it={it} />
        ) : (
          <Integration key={it.updated_at ?? "unsaved"} it={{ ...it, kind: it.kind }} />
        ))}
      {console && <Integration key={console.updated_at ?? "unsaved"} it={{ ...console, kind: "console" }} />}
      {it && kind === "vcenter" && <VCenters />}
      {it && kind === "awx" && <InventoryToken />}
      {it && kind === "nsx" && <NSXManagers />}
      {it && kind === "servicenow" && <ServiceNowToken />}
      {it && kind === "servicenow" && <ServiceNowCmdb />}
    </Page>
  );
}
