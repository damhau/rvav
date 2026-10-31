import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MOCK_ADMIN_PASSWORD } from "@/mocks/signIn";
import { ChangePasswordPage, SignInPage } from "./SignIn";

// The pages leave with a full page load: catch where they go.
let assign: ReturnType<typeof vi.fn>;
beforeEach(() => {
  assign = vi.fn();
  vi.stubGlobal("location", { ...globalThis.location, pathname: "/deployments", search: "?env=prd", assign });
});
afterEach(() => vi.unstubAllGlobals());

describe("the sign-in page", () => {
  it("offers single sign-on first, and the local administrator for when it is down", () => {
    render(<SignInPage provider />);
    expect(screen.getByRole("heading", { name: "Sign in to rvav" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in with single sign-on" })).toBeInTheDocument();
    const local = screen.getByRole("form", { name: "Local administrator" });
    expect(within(local).getByRole("button", { name: "Sign in as the local administrator" })).toBeDisabled();
  });

  it("says how to set up a provider when none is", () => {
    render(<SignInPage provider={false} />);
    expect(screen.queryByRole("button", { name: /single sign-on/ })).not.toBeInTheDocument();
    expect(screen.getByText(/No identity provider is set up yet/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
  });

  it("refuses a wrong password, waits after three, then goes to the password change", async () => {
    const user = userEvent.setup();
    render(<SignInPage provider={false} />);
    await user.type(screen.getByLabelText("Username"), "admin");
    const password = screen.getByLabelText("Password");
    const submit = screen.getByRole("button", { name: "Sign in" });

    for (let i = 0; i < 3; i++) {
      await user.type(password, `wrong-${i}`);
      await user.click(submit);
      expect(await screen.findByRole("alert")).toHaveTextContent("The username or password is wrong.");
      expect(password).toHaveValue("");
    }
    await user.type(password, MOCK_ADMIN_PASSWORD);
    await user.click(submit);
    expect(await screen.findByRole("alert")).toHaveTextContent(/Too many failed attempts in a row.*1 second left/);
    expect(submit).toBeDisabled();
    expect(assign).not.toHaveBeenCalled();

    // Once the wait is over, the right password goes on to the change.
    await waitFor(() => expect(screen.getByRole("alert")).not.toHaveTextContent(/left/), { timeout: 3000 });
    await user.type(password, MOCK_ADMIN_PASSWORD);
    expect(submit).toBeEnabled();
    await user.click(submit);
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/change-password"));
  });
});

describe("the password change", () => {
  it("checks the new password, then goes back to the page asked for", async () => {
    const user = userEvent.setup();
    render(<ChangePasswordPage />);
    const current = screen.getByLabelText("Current password");
    const next = screen.getByLabelText("New password");
    const repeat = screen.getByLabelText("New password again");
    const submit = screen.getByRole("button", { name: "Change password" });
    expect(next).toHaveAccessibleDescription("At least 12 characters, other than the current one.");

    await user.type(current, MOCK_ADMIN_PASSWORD);
    await user.type(next, "short");
    await user.click(submit);
    expect(next).toHaveAccessibleDescription("Use at least 12 characters.");

    await user.clear(next);
    await user.type(next, "a much longer password");
    await user.type(repeat, "a much longer passwort");
    await user.click(submit);
    expect(repeat).toHaveAccessibleDescription("The two new passwords differ.");

    // The server's checks: the current password is wrong.
    await user.clear(current);
    await user.type(current, "not the password");
    await user.clear(repeat);
    await user.type(repeat, "a much longer password");
    await user.click(submit);
    await waitFor(() => expect(current).toHaveAccessibleDescription("The current password is wrong."));
    expect(current).toBeInvalid();

    await user.clear(current);
    await user.type(current, MOCK_ADMIN_PASSWORD);
    await user.click(submit);
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/deployments?env=prd"));
  });
});
