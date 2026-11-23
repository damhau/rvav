// A filter's "no filter" choice says All, as every list's does.
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("a filter left unset", () => {
  it.each([
    ["/resources", "Power", "All power states"],
    ["/admin/catalog-items", "State", "All states"],
    ["/admin/subscriptions/sub_servicenow", "Status", "All statuses"],
  ])("on %s says %s: %s", async (path, label, all) => {
    renderApp(path);
    expect(await screen.findByRole("combobox", { name: label }, { timeout: 5000 })).toHaveTextContent(all);
  });
});
