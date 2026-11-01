import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("app shell", () => {
  it("names no vCenter or AWX of the mockup", async () => {
    // The sidebar ended with vc01.corp.example and the catalog said AWX jobs
    // ran on awx.corp.example, on every install: a new one set up from the UI
    // has its own (and rvav can have several vCenters).
    renderApp("/catalog");
    expect(
      await screen.findByText(/AWX items launch job templates in AWX\./, {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    const main = screen.getByRole("navigation", { name: "Main" });
    expect(main).not.toHaveTextContent(/corp\.example/);
    expect(document.body).not.toHaveTextContent(/vc01\.corp\.example|awx\.corp\.example/);
  });
});
