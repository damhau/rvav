// The token AWX's inventory source reads rvav with (U1.4, ADR-0030): rvav
// makes it for a user of its own, valid a year, puts it in the source's
// rvav API credential with the address AWX reaches rvav at, revokes the one
// before and syncs the source. RVAV_INVENTORY_TOKEN locks it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/utils";
import { failed, fieldErrors } from "./governance/common";

export function InventoryToken() {
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
      toast.success("The inventory token is in AWX");
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "No token made");
    },
  });
  if (q.isPending) return <Skeleton className="h-40" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const st = q.data;
  const t = st.token;
  const done = make.data;
  return (
    <Panel
      title="Inventory token"
      actions={st.locked ? <Badge tone="neutral">Locked</Badge> : !t ? <Badge tone="warn">None made</Badge> : undefined}
    >
      <form
        className="flex max-w-3xl flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          make.mutate();
        }}
      >
        <p className="text-[13px] text-muted">
          AWX&apos;s inventory source reads rvav&apos;s VMs with an API token that reads the inventory only, kept in its
          rvav API credential. rvav makes it for a user of its own, valid a year, and puts it in AWX; the one made
          before is revoked.
        </p>
        {st.locked ? (
          <p className="inline-flex items-center gap-1 text-xs text-faint">
            <Lock className="size-3" aria-hidden /> Set by RVAV_INVENTORY_TOKEN: rvav awx-bootstrap puts that one in
            AWX.
          </p>
        ) : (
          <>
            <p className="text-xs">
              {t ? (
                <>
                  Made <time dateTime={t.created_at}>{fullDate(t.created_at)}</time>
                  {t.expires_at && (
                    <>
                      , expires <time dateTime={t.expires_at}>{fullDate(t.expires_at)}</time>
                    </>
                  )}
                  ; {t.last_used_at ? `AWX last read the inventory ${relative(t.last_used_at)}` : "not read yet"}.
                </>
              ) : (
                "rvav hasn't made one: AWX reads the inventory with a token made by hand, or not at all."
              )}
            </p>
            <Field
              label="rvav's address, as AWX reaches it"
              htmlFor="inventory-url"
              error={errors["/url"]}
              hint={`Empty keeps the one in AWX's credential, or else ${st.public_url || "rvav's public URL"}`}
            >
              <Input
                id="inventory-url"
                className="font-mono text-xs"
                placeholder={st.public_url || undefined}
                value={url}
                onChange={(e) => setUrl(e.target.value)}
              />
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button type="submit" disabled={make.isPending || !st.source_set}>
                {make.isPending
                  ? "Making…"
                  : t
                    ? "Make a new token and put it in AWX"
                    : "Make a token and put it in AWX"}
              </Button>
              {!st.source_set && (
                <span className="text-xs text-faint">Set the inventory source ID above, and save, first.</span>
              )}
            </div>
            {done && (
              <section
                aria-label="Inventory token made"
                aria-live="polite"
                className="flex flex-col gap-0.5 rounded-md border border-line p-3 text-xs"
              >
                <p>
                  In AWX&apos;s credential <span className="font-medium">{done.credential}</span>
                  {done.attached ? ", now set on the inventory source" : ""}, reading rvav at{" "}
                  <span className="font-mono">{done.url}</span>.
                </p>
                {done.revoked ? <p>The token made before is revoked.</p> : null}
                {done.update && <p>{done.update}</p>}
              </section>
            )}
          </>
        )}
      </form>
    </Panel>
  );
}
