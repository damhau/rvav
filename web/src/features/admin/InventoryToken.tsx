// The token AWX's inventory source reads rvav with (U1.4, ADR-0030): rvav
// makes it for a user of its own, valid a year, puts it in the source's
// rvav API credential with the address AWX reaches rvav at, revokes the one
// before and syncs the source. RVAV_INVENTORY_TOKEN locks it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSaveFocus } from "@/components/ui/opener";
import { Field, Input } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed, fieldErrors } from "./governance/common";

export function InventoryToken() {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery(adm.inventoryToken());
  const [url, setUrl] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const make = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/integrations/awx/inventory-token", {
          body: url.trim() ? { url: url.trim() } : {},
        }),
      ),
    onSuccess: () => {
      setErrors({});
      void qc.invalidateQueries({ queryKey: ["admin", "inventory-token"] });
      toast.success(t("inventoryToken.madeToast"));
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("inventoryToken.noneMadeToast"));
    },
  });
  const form = useRef<HTMLFormElement>(null);
  const submit = useRef<HTMLButtonElement>(null);
  const saveFocus = useSaveFocus(form, make.isPending, () => submit.current);
  if (q.isPending) return <Skeleton className="h-40" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const st = q.data;
  const token = st.token;
  const done = make.data;
  return (
    <Panel
      title={t("inventoryToken.title")}
      actions={
        st.locked ? (
          <Badge tone="neutral">{t("inventoryToken.locked")}</Badge>
        ) : !token ? (
          <Badge tone="warn">{t("inventoryToken.noneMade")}</Badge>
        ) : undefined
      }
    >
      <form
        ref={form}
        className="flex max-w-3xl flex-col gap-3"
        onKeyDownCapture={saveFocus.interacted}
        onPointerDownCapture={saveFocus.interacted}
        onSubmit={(e) => {
          e.preventDefault();
          if (make.isPending) return;
          setErrors({});
          saveFocus.submitted();
          make.mutate();
        }}
      >
        <p className="text-body text-muted">{t("inventoryToken.about")}</p>
        {st.locked ? (
          <p className="inline-flex items-center gap-1 text-secondary text-faint">
            <Lock className="size-3" aria-hidden /> {t("inventoryToken.lockedBy")}
          </p>
        ) : (
          <>
            <p className="text-secondary">
              {token ? (
                <Trans
                  t={t}
                  i18nKey={token.expires_at ? "inventoryToken.madeExpires" : "inventoryToken.made"}
                  values={{
                    made: fullDate(token.created_at),
                    expires: token.expires_at ? fullDate(token.expires_at) : "",
                    read: token.last_used_at
                      ? t("inventoryToken.lastRead", { when: relative(token.last_used_at) })
                      : t("inventoryToken.notRead"),
                  }}
                  components={{
                    made: <time dateTime={token.created_at} />,
                    expires: <time dateTime={token.expires_at ?? undefined} />,
                  }}
                />
              ) : (
                t("inventoryToken.none")
              )}
            </p>
            <Field
              label={t("inventoryToken.url")}
              htmlFor="inventory-url"
              error={errors["/url"]}
              hint={t("inventoryToken.urlHint", { url: st.public_url || t("inventoryToken.publicUrl") })}
            >
              <Input
                id="inventory-url"
                className="font-mono text-secondary"
                placeholder={st.public_url || undefined}
                value={url}
                onChange={(e) => setUrl(e.target.value)}
              />
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button ref={submit} type="submit" disabled={make.isPending || !st.source_set}>
                {make.isPending
                  ? t("inventoryToken.making")
                  : token
                    ? t("inventoryToken.makeNew")
                    : t("inventoryToken.make")}
              </Button>
              {!st.source_set && <span className="text-secondary text-faint">{t("inventoryToken.sourceFirst")}</span>}
            </div>
            {done && (
              <section
                aria-label={t("inventoryToken.madeLabel")}
                aria-live="polite"
                className="flex flex-col gap-0.5 rounded-md border border-line p-3 text-secondary"
              >
                <p>
                  <Trans
                    t={t}
                    i18nKey={done.attached ? "inventoryToken.inCredentialAttached" : "inventoryToken.inCredential"}
                    values={{ credential: done.credential, url: done.url }}
                    components={{ name: <span className="font-medium" />, url: <span className="font-mono" /> }}
                  />
                </p>
                {done.revoked ? <p>{t("inventoryToken.revoked")}</p> : null}
                {done.update && <p>{done.update}</p>}
              </section>
            )}
          </>
        )}
      </form>
    </Panel>
  );
}
