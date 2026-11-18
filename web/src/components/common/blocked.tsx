import { Link } from "@tanstack/react-router";
import { MapPinOff, ShieldOff } from "lucide-react";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";

const toOverview = (
  <Button variant="primary" size="sm" asChild>
    <Link to="/">Go to the overview</Link>
  </Button>
);

/** An address that matches no page. */
export function PageNotFound() {
  return (
    <Page>
      <PageHeader title="Page not found" />
      <Panel>
        <EmptyState
          icon={MapPinOff}
          title="This address doesn't match anything in rvav"
          body="The page may have moved, or the link has a typo. Start again from the overview."
          action={toOverview}
        />
      </Panel>
    </Page>
  );
}

/** A page the signed-in person may not open; the server would refuse what it shows. */
export function PageNotAllowed({ page }: { page: string }) {
  return (
    <Page>
      <PageHeader title="You can't open this page" />
      <Panel>
        <EmptyState
          icon={ShieldOff}
          title={`${page} needs a role you don't have`}
          body={
            <>
              Ask an administrator for it if you need this page.{" "}
              <Link to="/access" className="text-accent underline underline-offset-2">
                What I may do
              </Link>{" "}
              lists what you can do.
            </>
          }
          action={toOverview}
        />
      </Panel>
    </Page>
  );
}
