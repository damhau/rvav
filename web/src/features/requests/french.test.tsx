import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { ApprovalPreview, PlacementConstraint } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { chosenBy, imageText } from "./ComponentsPreview";
import { constraintText } from "./constraints";
import { limitLabel } from "./limits";
import { quotaLabel, quotaWhere } from "./quota";
import { approvalBody, approvalTitle } from "./RequestFormPage";
import { repeatText } from "./SchedulesPage";

const k: PlacementConstraint = {
  kind: "compute",
  tag: "rvav-env/prod",
  negated: false,
  soft: false,
  met: true,
  excluded: [],
};

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});

describe("the request form's helpers, in French", () => {
  it("says what a constraint chose, ruled out or let go", () => {
    expect(constraintText({ ...k, object: "GVA-PRD-CL01", excluded: ["GVA-DEV-CL01"] })).toBe(
      "rvav-env/prod\u00a0: cluster GVA-PRD-CL01, écartant GVA-DEV-CL01",
    );
    expect(constraintText({ ...k, tag: "rvav-zone/pci", negated: true, object: "GVA-PRD-CL01" })).toBe(
      "non rvav-zone/pci\u00a0: cluster GVA-PRD-CL01",
    );
    const gold = { ...k, kind: "storage", tag: "rvav-tier/gold", soft: true } as const;
    expect(constraintText({ ...gold, met: false, object: "nfs-gva-silver-02" })).toBe(
      "rvav-tier/gold (préférée)\u00a0: abandonnée, datastore nfs-gva-silver-02 ne la respecte pas",
    );
    expect(constraintText({ ...k, kind: "storage", tag: "rvav-tier/gold", disk: 1, object: "vsan-gva-prd-cl01" })).toBe(
      "rvav-tier/gold\u00a0: datastore du disque 2 vsan-gva-prd-cl01",
    );
    expect(constraintText({ ...k, tag: "rvav-env/test", met: false })).toBe(
      "rvav-env/test\u00a0: aucun cluster autorisé ne la respecte",
    );
  });

  it("names quotas, limits and where they count, read in the language shown", () => {
    expect(quotaLabel.memory_gb).toEqual(["Mémoire", "GB"]);
    expect(limitLabel.deployments_per_user).toEqual(["Déploiements par utilisateur", ""]);
    expect(quotaWhere({ level: "environment", environment: "dev" })).toBe(" en dev");
    expect(quotaWhere({ level: "global_user" })).toBe(" · par utilisateur, tout rvav");
  });

  it("says the vCenters a template isn't in, and who chose the cluster", () => {
    expect(imageText({ key: "ubuntu", label: "Ubuntu 22.04", excluded: ["vc-gva", "vc-zrh"] })).toBe(
      "Ubuntu 22.04\u00a0: aucun modèle dans vCenter vc-gva, vc-zrh, écarté",
    );
    expect(chosenBy({ chosen: true, strategy: "spread" })).toBe("choisi par rvav, spread");
  });

  it("says who approves and how long it usually takes", () => {
    const approval: ApprovalPreview = {
      required: true,
      policies: [
        {
          policy_id: "pol_1",
          name: "Production",
          reason: "Production VMs need sign-off.",
          levels: [
            { name: "Infrastructure leads", approvers: [], mode: "any" },
            { approvers: [], mode: "any" },
          ],
        },
      ],
      typical_wait_seconds: 60,
    };
    expect(approvalTitle(approval)).toBe("Approbation requise\u00a0: Infrastructure leads, puis un approbateur");
    expect(approvalBody(approval)).toBe("Production VMs need sign-off. Décision habituelle en moins de 1 minute.");
    expect(approvalBody({ ...approval, typical_wait_seconds: 7200 })).toBe(
      "Production VMs need sign-off. Décision habituelle en moins de 2 heures.",
    );
  });

  it("says how often a schedule runs, its unit agreeing", () => {
    expect(repeatText({ every_minutes: 60, timezone: "UTC" })).toBe("Toutes les heures");
    expect(repeatText({ every_minutes: 1440 * 2, timezone: "UTC" })).toBe("Tous les 2 jours");
    expect(repeatText({ every_minutes: 90, timezone: "UTC" })).toBe("Toutes les 90 minutes");
  });
});

describe("the requests' pages, in French", () => {
  beforeEach(() => {
    db.me.locale = "fr";
  });

  it("lists the requests with their filters in French", async () => {
    renderApp("/requests");
    expect(await screen.findByRole("heading", { name: "Demandes", level: 1 }, { timeout: 5000 })).toBeVisible();
    expect(screen.getByRole("link", { name: "Nouvelle demande" })).toHaveAttribute("href", "/catalog");
    const table = await screen.findByRole("table", { name: "Demandes" });
    expect(within(table).getByRole("columnheader", { name: "Demandée par" })).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Rechercher des demandes" })).toBeInTheDocument();
  });

  it("asks for an item with a French form, down to its submit button", { timeout: 15_000 }, async () => {
    renderApp("/catalog/rhel9-server/request");
    expect(
      await screen.findByRole("heading", { name: "Demander RHEL 9 server", level: 1 }, { timeout: 5000 }),
    ).toBeVisible();
    expect(screen.getByRole("radiogroup", { name: "Environnement" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Bail et motif" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Motif" })).toHaveAttribute(
      "placeholder",
      "À quoi cela sert-il\u00a0? Ajoutez un numéro de ticket s'il y en a un.",
    );
    expect(screen.getByRole("button", { name: "Enregistrer comme brouillon" })).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: /^Soumettre (la demande|pour approbation)$/ }),
    ).toBeInTheDocument();
  });

  it("says in a request's header who asked and where it stands", { timeout: 15_000 }, async () => {
    renderApp("/requests/REQ-10461");
    const title = await screen.findByRole("heading", { level: 1, name: /web-tst-search002/ }, { timeout: 5000 });
    const header = title.closest("header")!;
    expect(within(header).getByText(/^Les \d+ étapes terminées/)).toBeVisible();
    expect(within(header).getByRole("link", { name: "Ouvrir le déploiement" })).toBeVisible();
    expect(within(header).getByRole("link", { name: "Redemander" })).toBeVisible();
    expect(screen.getByRole("region", { name: "Ce qui a été demandé" })).toBeVisible();
  });

  // A Trans tag named like a void HTML element (<link>) would drop its text.
  it("links an update's request to its deployment, named", { timeout: 15_000 }, async () => {
    const r = db.requests.find((x) => x.request.number === "REQ-10477")!.request;
    Object.assign(r, { kind: "update", deployment_id: "dep_front", title: "Update · web-prd-front004" });
    renderApp("/requests/REQ-10477");
    const title = await screen.findByRole("heading", { level: 1, name: /web-prd-front004/ }, { timeout: 5000 });
    const header = title.closest("header")!;
    expect(within(header).getByRole("link", { name: "web-prd-front004" })).toHaveAttribute(
      "href",
      "/deployments/dep_front",
    );
    expect(within(header).getByText(/^Vers RHEL 9 server/)).toBeVisible();
  });
});
