// The certificate a connection's server presented to its test (U1.4,
// ADR-0030), and whether rvav trusts it. When it doesn't, the two ways to:
// pinning it for this connection by its SHA-256 (a self-signed one), or
// adding its CA to the CA bundle, which every connection trusts.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert, ShieldCheck, ShieldOff } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed } from "./governance/common";

type S = components["schemas"];

const day = (iso: string) =>
  new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" });

/** The bundle with a certificate added at its end. */
export function withCertificate(bundle: string, pem: string): string {
  return [bundle.trim(), pem.trim()].filter(Boolean).join("\n") + "\n";
}

export function ServerCertificate({
  cert,
  onTrust,
  pinInForm,
  pinLocked,
}: {
  cert: S["ServerCertificate"];
  /** Pins it in the form: the connection takes a pin. */
  onTrust?: (sha256: string) => void;
  /** Its SHA-256 is in the form, not saved yet. */
  pinInForm?: boolean;
  /** The variable that sets the pin, when one does. */
  pinLocked?: string;
}) {
  const qc = useQueryClient();
  const list = useQuery(adm.integrations());
  const trust = list.data?.items.find((it) => it.kind === "trust");
  const bundleLocked = trust?.locked.includes("ca_bundle") ?? false;
  const [confirming, setConfirming] = useState(false);
  const anchor = cert.anchor_is_leaf ? "this certificate" : cert.anchor_subject;
  const add = useMutation({
    mutationFn: () => {
      const now = typeof trust?.settings.ca_bundle === "string" ? trust.settings.ca_bundle : "";
      return call(
        api.PUT("/admin/integrations/{kind}", {
          params: { path: { kind: "trust" } },
          body: { settings: { ca_bundle: withCertificate(now, cert.anchor_pem) } },
        }),
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "integrations"] });
      setConfirming(false);
      toast.success(cert.anchor_is_leaf ? "Certificate added to the CA bundle" : `${anchor} added to the CA bundle`, {
        description: "Test again to check. Each process applies the bundle once its running work is done.",
      });
    },
    onError: (e) => failed(e, "Not added to the CA bundle"),
  });
  const vouched = cert.trusted || cert.pinned;
  const status = !cert.checked ? (
    <>
      <ShieldOff className="mt-px size-3.5 shrink-0 text-warn" aria-hidden />
      Not checked: TLS verification is off for this connection.
      {vouched ? " rvav would trust it." : " rvav wouldn't trust it: " + (cert.problem ?? "") + "."}
    </>
  ) : cert.trusted ? (
    <>
      <ShieldCheck className="mt-px size-3.5 shrink-0 text-ok" aria-hidden />A CA rvav trusts vouches for it.
    </>
  ) : cert.pinned ? (
    <>
      <ShieldCheck className="mt-px size-3.5 shrink-0 text-ok" aria-hidden />
      Pinned: trusted for this connection by its SHA-256.
    </>
  ) : (
    <>
      <ShieldAlert className="mt-px size-3.5 shrink-0 text-bad" aria-hidden />
      Not trusted: {cert.problem}.
    </>
  );
  return (
    <section aria-label="Server certificate" className="flex flex-col gap-2 rounded-md border border-line p-3 text-xs">
      <p className="font-medium">Certificate of {cert.address}</p>
      <p className="flex items-start gap-1.5">{status}</p>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-0.5">
        <dt className="text-muted">Issued to</dt>
        <dd>{cert.subject}</dd>
        <dt className="text-muted">Issued by</dt>
        <dd>{cert.issuer}</dd>
        <dt className="text-muted">Valid</dt>
        <dd>
          {day(cert.not_before)} to {day(cert.not_after)}
        </dd>
        {cert.names.length > 0 && (
          <>
            <dt className="text-muted">For</dt>
            <dd className="break-all">{cert.names.join(", ")}</dd>
          </>
        )}
        <dt className="text-muted">SHA-256</dt>
        <dd className="font-mono break-all">{cert.sha256}</dd>
      </dl>
      {!vouched && !confirming && (
        <div className="flex flex-wrap items-center gap-2">
          {cert.pinnable && onTrust && !pinInForm && (
            <Button type="button" size="sm" disabled={Boolean(pinLocked)} onClick={() => onTrust(cert.sha256)}>
              Trust this certificate
            </Button>
          )}
          {pinInForm && <span>Its SHA-256 is in the form: save the settings to trust it.</span>}
          <Button type="button" size="sm" disabled={bundleLocked || !trust} onClick={() => setConfirming(true)}>
            {cert.anchor_is_leaf ? "Add it to the CA bundle" : `Add ${anchor} to the CA bundle`}
          </Button>
          {pinLocked && <span className="text-faint">The pin is set by {pinLocked}.</span>}
          {bundleLocked && <span className="text-faint">The CA bundle is set by RVAV_CA_BUNDLE.</span>}
        </div>
      )}
      {confirming && (
        <div role="group" aria-label="Add to the CA bundle" className="flex flex-col gap-2">
          <p>
            {cert.anchor_is_leaf
              ? "Every connection will trust this certificate, not only this one's."
              : `Every connection will trust the certificates ${anchor} signs, not only this one's.`}
          </p>
          <div className="flex gap-2">
            <Button type="button" size="sm" variant="primary" disabled={add.isPending} onClick={() => add.mutate()}>
              Add to the CA bundle
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
