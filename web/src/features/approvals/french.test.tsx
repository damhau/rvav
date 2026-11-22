import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const front = "RHEL 9 server · web-prd-front004";

beforeEach(() => {
  db.me.locale = "fr";
});

describe("approvals, in French", () => {
  it("lists what waits, counted in French, and decides in French", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    expect(await screen.findByRole("heading", { name: "Approbations", level: 1 }, { timeout: 5000 })).toBeVisible();
    const table = await screen.findByRole("table", { name: "En attente de vous" });
    expect(await screen.findByText("2 en attente, les plus anciennes en premier")).toBeInTheDocument();
    expect(within(table).getByRole("columnheader", { name: "Demandée par" })).toBeInTheDocument();

    await user.click(await within(table).findByRole("link", { name: front }));
    const sheet = await screen.findByRole("dialog", { name: front });
    expect(await within(sheet).findByText(/^Pourquoi elle vous est soumise :/)).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Rejeter" }));
    const comment = within(sheet).getByLabelText("Commentaire");
    expect(comment).toHaveAccessibleDescription(/Dites pourquoi vous la rejetez/);
    await waitFor(() => expect(comment).toHaveFocus());
  });
});
