import { afterEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { targetTypeLabel } from "./AuditPage";

afterEach(async () => {
  await i18n.changeLanguage("en");
});

describe("what an audit event was on", () => {
  it("is named in the language shown", async () => {
    expect(targetTypeLabel("catalog_item")).toBe("catalog item");
    expect(targetTypeLabel("deployment_limit_policy")).toBe("deployment limit policy");
    expect(targetTypeLabel("api_token")).toBe("API token");
    await i18n.changeLanguage("fr");
    expect(targetTypeLabel("catalog_item")).toBe("élément de catalogue");
    expect(targetTypeLabel("lease_policy")).toBe("politique de bail");
    expect(targetTypeLabel("api_token")).toBe("jeton d'API");
  });

  it("is a type newer than the page as the server names it, every underscore a space", async () => {
    await i18n.changeLanguage("fr");
    expect(targetTypeLabel("cloud_zone_link")).toBe("cloud zone link");
    expect(targetTypeLabel("constructor")).toBe("constructor");
  });
});
