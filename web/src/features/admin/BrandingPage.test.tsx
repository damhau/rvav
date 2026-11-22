import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { applyBranding, rvavBranding } from "@/lib/branding";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The start of a PNG as the mock reads it: its signature, then its header with the size. */
function png(width: number, height: number) {
  const b = new Uint8Array(33);
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52]);
  new DataView(b.buffer).setUint32(16, width);
  new DataView(b.buffer).setUint32(20, height);
  return new File([b], "logo.png", { type: "image/png" });
}

const auditedActions = () =>
  (db as unknown as { governance?: { audit: { action: string }[] } }).governance?.audit.map((a) => a.action) ?? [];

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  applyBranding(rvavBranding);
  resetDb();
});

describe("Branding", () => {
  it(
    "refuses an accent that doesn't read, offers the closest that does, and puts it in the shell",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/branding");
      expect(await screen.findByRole("heading", { name: "Branding", level: 1 })).toBeInTheDocument();
      expect(await screen.findByText("rvav's own accent, in both themes.")).toBeInTheDocument();

      await user.type(screen.getByLabelText("Portal name"), "Corp Cloud");
      const accent = screen.getByLabelText("Accent colour");
      await user.type(accent, "#ffcc00");
      expect(
        await screen.findByText(
          /^#ffcc00 has a contrast of 1\.\d+:1 in the light theme: text needs 4\.5:1\. #[0-9a-f]{6} is the closest/,
        ),
      ).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Save branding" }));
      expect(accent).toHaveAccessibleDescription(/text needs 4\.5:1/);
      expect(accent).toBeInvalid();

      const use = await screen.findByRole("button", { name: /^Use #[0-9a-f]{6}$/ });
      const suggestion = use.textContent.replace("Use ", "");
      await user.click(use);
      expect(accent).toHaveValue(suggestion);
      expect(await screen.findByText(/^Reads at \d+\.\d+:1 in the light theme/)).toBeInTheDocument();
      expect(screen.getAllByRole("figure")[0]).toHaveTextContent(`Light theme ${suggestion}`);

      await user.click(screen.getByRole("button", { name: "Save branding" }));
      expect(await screen.findByText("Branding saved")).toBeInTheDocument();
      expect(document.title).toBe("Corp Cloud");
      const css = document.getElementById("rvav-branding")?.textContent ?? "";
      expect(css).toContain(`html:root { --accent: ${suggestion};`);
      expect(css).toContain('html:root[data-theme="dark"] { --accent: #');
      expect(within(screen.getByRole("navigation", { name: "Main" })).getByText("Corp Cloud")).toBeInTheDocument();
      expect(auditedActions()).toContain("settings.branding.update");
    },
  );

  it(
    "uploads a logo for each theme, refuses an SVG that runs a script, and removes one",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/branding");
      const upload = await screen.findByRole("button", { name: "Upload the light theme's logo" });
      expect(upload).toHaveAccessibleDescription("No logo: rvav's mark is shown.");
      const [light, dark] = document.querySelectorAll<HTMLInputElement>('input[type="file"]');

      await user.upload(light!, png(240, 64));
      expect(await screen.findByText("Light theme's logo saved")).toBeInTheDocument();
      expect(screen.getByRole("img", { name: "Light theme's logo" })).toHaveAttribute(
        "src",
        expect.stringMatching(/^\/api\/v1\/branding\/logos\/light\?v=[0-9a-f]{16}$/),
      );
      expect(screen.getByRole("button", { name: "Upload the dark theme's logo" })).toHaveAccessibleDescription(
        "No logo: The light theme's logo is shown.",
      );

      await user.upload(
        dark!,
        new File(['<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>'], "logo.svg", {
          type: "image/svg+xml",
        }),
      );
      expect(screen.getByRole("button", { name: "Upload the dark theme's logo" })).toHaveAccessibleDescription(
        "The SVG has a <script> element, which a logo can't have.",
      );
      await user.upload(dark!, png(4000, 64));
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Upload the dark theme's logo" })).toHaveAccessibleDescription(
          /4000×64 pixels/,
        ),
      );

      await user.click(screen.getByRole("button", { name: "Remove the light theme's logo" }));
      expect(await screen.findByText("Light theme's logo removed")).toBeInTheDocument();
      expect(screen.queryByRole("img", { name: "Light theme's logo" })).not.toBeInTheDocument();
      expect(auditedActions()).toEqual(
        expect.arrayContaining(["settings.branding_logo.set", "settings.branding_logo.remove"]),
      );
    },
  );

  it("is for those who manage the system", { timeout: 15_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/branding");
    expect(await screen.findByText("Branding needs a role you don't have")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Branding" })).not.toBeInTheDocument();
  });
});
