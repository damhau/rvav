import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// A certificate's validity in the reader's time zone (#211). Fourteen hours
// ahead of UTC, the mock's certificates, valid until noon UTC, are valid
// until the next day; the server sends the instant, the page the day.
const zone = process.env.TZ;
beforeAll(() => {
  process.env.TZ = "Pacific/Kiritimati";
});
afterAll(() => {
  if (zone === undefined) delete process.env.TZ;
  else process.env.TZ = zone;
});

function open(kind: string) {
  renderApp(`/admin/integrations/${kind}`);
  return userEvent.setup();
}

describe("a certificate's dates, in the reader's time zone", () => {
  it("in English", async () => {
    const user = open("nsx");
    const nsx = await screen.findByRole("region", { name: "NSX connection" });
    await user.click(within(nsx).getByRole("button", { name: "Test connection" }));
    const test = await screen.findByRole("region", { name: "NSX test" });
    expect(
      await within(test).findByText(/until 16 January 2027: a CA rvav trusts vouches for it\.$/),
    ).toBeInTheDocument();
    expect(within(test).getByRole("region", { name: "Server certificate" })).toHaveTextContent("16 January 2027");
    expect(test).not.toHaveTextContent("15 January 2027");
    expect(test).not.toHaveTextContent("2027-01-15T");
  });

  describe("in French", () => {
    beforeEach(async () => {
      db.me.locale = "fr";
      await i18n.changeLanguage("fr");
    });

    it("the bundle's CAs, through the shared date format", async () => {
      const user = open("trust");
      const bundle = await screen.findByRole("region", { name: "Bundle d'AC" });
      await user.type(
        within(bundle).getByRole("textbox", { name: "Certificats d'AC (PEM)" }),
        "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
      );
      await user.click(within(bundle).getByRole("button", { name: "Vérifier" }));
      expect(await within(bundle).findByText(/^A CA, until 16 janvier 2031; SHA-256/)).toBeInTheDocument();
      expect(bundle).not.toHaveTextContent("2031-01-15T");
    });
  });
});
