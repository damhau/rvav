import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TrialWindowPage } from "@/features/account/SignIn";
import { TRIAL_CHANNEL } from "@/lib/auth";
import { renderApp } from "@/test/render";

// The identity provider in Administration (ADR-0030), against the mock API.

afterEach(() => localStorage.clear());

async function panel() {
  renderApp("/admin/integrations");
  return screen.findByRole("region", { name: "Identity provider" });
}

/** The trial window, which sends its token back over the channel. */
function trialWindow(message: { token?: string; error?: string }) {
  return vi.spyOn(window, "open").mockImplementation(() => {
    setTimeout(() => {
      const ch = new BroadcastChannel(TRIAL_CHANNEL);
      ch.postMessage(message);
      ch.close();
    }, 10);
    return {} as Window;
  });
}

describe("the identity provider", () => {
  it("comes first, with the redirect URI to register", async () => {
    const p = await panel();
    expect(screen.getAllByRole("tab")[0]).toHaveTextContent("Identity provider");
    expect(within(p).getByLabelText("Issuer")).toHaveValue("https://login.corp.example/realms/rvav");
    expect(within(p).getByLabelText("Client ID")).toHaveValue("rvav");
    expect(within(p).getByText(`${location.origin}/auth/callback`)).toBeInTheDocument();
    // Tried only once a test reached the provider.
    expect(within(p).getByRole("button", { name: "Try signing in" })).toBeDisabled();
  });

  it("is tested, then tried with a sign-in that shows the claims and the roles they give", async () => {
    const user = userEvent.setup();
    const p = await panel();
    await user.click(within(p).getByRole("button", { name: "Test provider" }));
    const test = await within(p).findByRole("region", { name: "Identity provider test" });
    expect(test).toHaveTextContent("Every check passed");
    expect(test).toHaveTextContent("Signing keys: 1 published: k1 (RS256)");

    const open = trialWindow({ token: "mock-trial-token" });
    await user.click(within(p).getByRole("button", { name: "Try signing in" }));
    expect(open).toHaveBeenCalledWith("/auth/trial", "rvav-oidc-trial", expect.stringContaining("popup"));
    // The window reads the settings tried from the browser's storage.
    expect(JSON.parse(localStorage.getItem("rvav.oidcTrial")!)).toEqual({
      authority: "https://login.corp.example/realms/rvav",
      clientId: "rvav",
      scope: "openid profile email",
    });
    const trial = await within(p).findByRole("region", { name: "Trial sign-in" });
    expect(trial).toHaveTextContent("rvav accepts this token.");
    expect(trial).toHaveTextContent("Ada Lovelace");
    expect(within(trial).getByText("rvav-web")).toBeInTheDocument();
    expect(within(trial).getByText("catalog-admin")).toBeInTheDocument();
    expect(within(trial).getByText(/member in /)).toBeInTheDocument();
    expect(within(trial).getByText("The token's claims")).toBeInTheDocument();
  });

  it("says why a token is refused, and needs a new test once the settings change", async () => {
    const user = userEvent.setup();
    const p = await panel();
    await user.click(within(p).getByRole("button", { name: "Test provider" }));
    await within(p).findByRole("region", { name: "Identity provider test" });
    const client = within(p).getByLabelText("Client ID");
    await user.clear(client);
    await user.type(client, "rvav-web");
    expect(within(p).getByRole("button", { name: "Try signing in" })).toBeDisabled();

    await user.click(within(p).getByRole("button", { name: "Test provider" }));
    await waitFor(() => expect(within(p).getByRole("button", { name: "Try signing in" })).toBeEnabled());
    trialWindow({ token: "mock-trial-token" });
    await user.click(within(p).getByRole("button", { name: "Try signing in" }));
    const trial = await within(p).findByRole("region", { name: "Trial sign-in" });
    expect(trial).toHaveTextContent("rvav refuses this token.");
    expect(trial).toHaveTextContent(/expects the client ID as the audience/);
    expect(within(trial).queryByText("Roles given")).not.toBeInTheDocument();
    // Nothing was saved: a new page has the saved client.
    cleanup();
    expect(within(await panel()).getByLabelText("Client ID")).toHaveValue("rvav");
  });

  it("says when the provider can't be reached, the window was blocked, or the sign-in failed", async () => {
    const user = userEvent.setup();
    const p = await panel();
    const issuer = within(p).getByLabelText("Issuer");
    await user.clear(issuer);
    await user.type(issuer, "https://unreachable.corp.example/realms/rvav");
    await user.click(within(p).getByRole("button", { name: "Test provider" }));
    expect(await within(p).findByRole("region", { name: "Identity provider test" })).toHaveTextContent(
      "Discovery: Get",
    );
    expect(within(p).getByRole("button", { name: "Try signing in" })).toBeDisabled();

    await user.clear(issuer);
    await user.type(issuer, "https://login.corp.example/realms/rvav");
    await user.click(within(p).getByRole("button", { name: "Test provider" }));
    await waitFor(() => expect(within(p).getByRole("button", { name: "Try signing in" })).toBeEnabled());
    vi.spyOn(window, "open").mockReturnValue(null);
    await user.click(within(p).getByRole("button", { name: "Try signing in" }));
    expect(within(p).getByRole("alert")).toHaveTextContent("The browser blocked the window");
    expect(localStorage.getItem("rvav.oidcTrial")).toBeNull();

    trialWindow({ error: "AADSTS50011: The redirect URI does not match." });
    await user.click(within(p).getByRole("button", { name: "Try signing in" }));
    expect(await within(p).findByText("AADSTS50011: The redirect URI does not match.")).toBeInTheDocument();
  });

  it("refuses scopes without openid, and saves without a restart", async () => {
    const user = userEvent.setup();
    const p = await panel();
    const scopes = within(p).getByLabelText("Scopes");
    await user.clear(scopes);
    await user.type(scopes, "profile email");
    await user.click(within(p).getByRole("button", { name: "Save settings" }));
    expect(await within(p).findByText("The scopes must include openid.")).toBeInTheDocument();

    await user.clear(scopes);
    await user.type(scopes, "openid profile email offline_access");
    await user.click(within(p).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("Identity provider saved")).toBeInTheDocument();
    // The panel shows the saved settings.
    expect(await screen.findByText(/^Saved /)).toBeInTheDocument();
    expect(screen.queryByText("Applying")).not.toBeInTheDocument();
    cleanup();
    expect(within(await panel()).getByLabelText("Scopes")).toHaveValue("openid profile email offline_access");
  });
});

describe("the trial window", () => {
  it("says the token went back, or why the sign-in failed", () => {
    const { unmount } = render(<TrialWindowPage result={{ token: "t" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("Signed in at the provider");
    unmount();
    render(<TrialWindowPage result={{ error: "access_denied" }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("The trial sign-in failed: access_denied");
  });
});
