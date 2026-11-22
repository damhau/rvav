// The server's names for what rvav builds in, against locales/en/names.json:
// the mocks' copies of the server's fixtures (roles.json, the notification
// templates) and the mocks' day-2 catalogue, which tests keep as the server's.
import { afterEach, describe, expect, it } from "vitest";
import { catalogue as day2Catalogue } from "@/mocks/day2";
import templates from "@/mocks/notificationTemplates.json";
import roles from "@/mocks/roles.json";
import { i18n } from ".";
import {
  areaName,
  day2Group,
  day2Name,
  kindName,
  kindWhen,
  permissionSummary,
  roleName,
  serverText,
  stepTitle,
} from "./names";

const french = () => i18n.changeLanguage("fr");

afterEach(async () => {
  await i18n.changeLanguage("en");
});

describe("the server's names in English", () => {
  it("are the server's own words, so they show as they are", () => {
    for (const r of roles.roles) expect(roleName(r)).toBe(r.name);
    for (const p of roles.catalogue) {
      expect(areaName(p.area)).toBe(p.area);
      expect(permissionSummary(p)).toBe(p.summary);
      expect(i18n.getResource("en", "names", `permission.${p.id.replaceAll(".", "_")}`)).toBe(p.summary);
    }
    for (const r of roles.roles) expect(i18n.getResource("en", "names", `role.${r.key}`)).toBe(r.name);
    for (const k of templates.kinds) {
      expect(i18n.getResource("en", "names", `kind.${k.kind}.name`)).toBe(k.name);
      expect(i18n.getResource("en", "names", `kind.${k.kind}.when`)).toBe(k.when);
    }
    for (const a of day2Catalogue.filter((x) => !x.key.includes(".")))
      expect(i18n.getResource("en", "names", `day2.${a.key}`), a.key).toBe(a.name);
  });
});

describe("the server's names in French", () => {
  it("word every built-in role, permission and notification kind", async () => {
    await french();
    for (const r of roles.roles) expect(roleName(r)).toBe(i18n.getResource("fr", "names", `role.${r.key}`));
    expect(roleName({ key: "catalog-admin", name: "Catalog admin" })).toBe("Administrateur du catalogue");
    expect(areaName("Projects")).toBe("Projets");
    expect(areaName("Day-2 actions")).toBe("Actions de jour 2");
    expect(permissionSummary({ id: "request.on_behalf", summary: "Request for someone else." })).toBe(
      "Demander pour quelqu'un d'autre.",
    );
    for (const p of roles.catalogue) expect(permissionSummary(p), p.id).not.toBe(p.summary);
    for (const k of templates.kinds) {
      expect(kindName(k), k.kind).not.toBe(k.name);
      expect(kindWhen(k), k.kind).not.toBe(k.when);
    }
    expect(kindName({ kind: "request_rolled_back", name: "Request rolled back" })).toBe(
      "Retour en arrière d'une demande",
    );
  });

  it("word built-in day-2 actions and groups, and leave an AWX action as named", async () => {
    await french();
    expect(day2Name({ key: "power_on", name: "Power on" })).toBe("Allumer");
    expect(day2Name({ key: "nsx_group_add", name: "Add to a security group" })).toBe("Ajouter à un groupe de sécurité");
    expect(day2Name({ key: "awx.install-monitoring-agent", name: "Install monitoring agent" })).toBe(
      "Install monitoring agent",
    );
    expect(day2Group("power")).toBe("alimentation");
    expect(day2Group("lifecycle")).toBe("cycle de vie");
  });

  it("word a request's own steps, by key or by title alone, and leave the rest", async () => {
    await french();
    expect(stepTitle({ key: "submit", title: "Submitted" })).toBe("Soumise");
    expect(stepTitle({ key: "request.submit", title: "Submitted" })).toBe("Soumise");
    expect(stepTitle({ key: "policy", title: "Waiting for approval" })).toBe("En attente d'approbation");
    expect(stepTitle({ key: "policy.approval", title: "Approval not required" })).toBe("Approbation non requise");
    expect(stepTitle({ key: "ready", title: "Done" })).toBe("Terminée");
    expect(stepTitle({ title: "Waiting for approval" })).toBe("En attente d'approbation");
    expect(stepTitle({ key: "policy", title: "Approved by Marc Keller" })).toBe("Approved by Marc Keller");
    expect(stepTitle({ key: "vm/web01/clone", title: "Ready" })).toBe("Ready");
    expect(stepTitle({ title: "Clone template · web01" })).toBe("Clone template · web01");
  });

  it("fall back to the server's words for anything else", async () => {
    await french();
    expect(roleName({ key: "ops", name: "Operations" })).toBe("Operations");
    expect(roleName({ key: "admin", name: "Boss" })).toBe("Boss");
    expect(roleName({ key: "constructor", name: "Odd" })).toBe("Odd");
    expect(roleName({ key: "__proto__", name: "Odder" })).toBe("Odder");
    expect(areaName("Billing")).toBe("Billing");
    expect(permissionSummary({ id: "billing.read", summary: "Read the bills." })).toBe("Read the bills.");
    expect(kindName({ kind: "quota_reached", name: "Quota reached" })).toBe("Quota reached");
    expect(kindWhen({ kind: "request_submitted", when: "To someone new." })).toBe("To someone new.");
    expect(day2Group("billing")).toBe("billing");
    expect(serverText("admin", "setup.detail.fixed", "https://vc.example")).toBe("https://vc.example");
  });

  it("follow the language back to English", async () => {
    await french();
    expect(roleName({ key: "viewer", name: "Viewer" })).toBe("Lecteur");
    await i18n.changeLanguage("en");
    expect(roleName({ key: "viewer", name: "Viewer" })).toBe("Viewer");
  });
});
