import { Link } from "@tanstack/react-router";
import { MapPinOff, ShieldOff } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";

function ToOverview() {
  const { t } = useTranslation();
  return (
    <Button variant="primary" size="sm" asChild>
      <Link to="/">{t("blocked.toOverview")}</Link>
    </Button>
  );
}

/** An address that matches no page. */
export function PageNotFound() {
  const { t } = useTranslation();
  return (
    <Page>
      <PageHeader title={t("blocked.notFound.title")} />
      <Panel>
        <EmptyState
          icon={MapPinOff}
          title={t("blocked.notFound.heading")}
          body={t("blocked.notFound.body")}
          action={<ToOverview />}
        />
      </Panel>
    </Page>
  );
}

/** A page the signed-in person may not open; the server would refuse what it shows. */
export function PageNotAllowed({ page }: { page: string }) {
  const { t } = useTranslation();
  return (
    <Page>
      <PageHeader title={t("blocked.notAllowed.title")} />
      <Panel>
        <EmptyState
          icon={ShieldOff}
          title={t("blocked.notAllowed.heading", { page })}
          body={
            <Trans
              i18nKey="blocked.notAllowed.body"
              components={{
                access: <Link to="/access" className="text-accent underline underline-offset-2" />,
              }}
            />
          }
          action={<ToOverview />}
        />
      </Panel>
    </Page>
  );
}
