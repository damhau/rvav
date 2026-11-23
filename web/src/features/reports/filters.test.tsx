// The reports' filters live in the address: a link opens the report
// filtered, and Clear filters goes back to the last 30 days, everywhere.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

const filtersBar = () => screen.getByRole("group", { name: "Report filters" });

describe("the reports' filters", () => {
  it("open from a link as it names them, and change in the address", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/reports?period=12m&project=prj_data");
    expect(await screen.findByRole("region", { name: "Quota of Data Engineering" }, { timeout: 5000 })).toBeVisible();
    const bar = filtersBar();
    expect(within(bar).getByRole("combobox", { name: "Period" })).toHaveTextContent("Last 12 months");
    expect(within(bar).getByRole("combobox", { name: "Interval" })).toHaveTextContent("By month");
    expect(within(bar).getByRole("combobox", { name: "Project" })).toHaveTextContent("Data Engineering");
    expect(within(bar).getByRole("combobox", { name: "Environment" })).toHaveTextContent("All environments");
    expect(screen.queryByRole("region", { name: "Quota of Web Platform" })).toBeNull();

    await user.click(within(bar).getByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Production" }));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ period: "12m", project: "prj_data", env: "prd" }),
    );

    await user.click(within(bar).getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    expect(within(bar).getByRole("combobox", { name: "Period" })).toHaveTextContent("Last 30 days");
    expect(within(bar).getByRole("combobox", { name: "Interval" })).toHaveTextContent("By day");
    expect(within(bar).getByRole("combobox", { name: "Project" })).toHaveTextContent("All projects");
    expect(await screen.findByRole("region", { name: "Quota of Web Platform" })).toBeVisible();
  });

  it("start a new period by its own interval", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/reports?by=week");
    const bar = await screen.findByRole("group", { name: "Report filters" }, { timeout: 5000 });
    expect(within(bar).getByRole("combobox", { name: "Interval" })).toHaveTextContent("By week");
    await user.click(within(bar).getByRole("combobox", { name: "Period" }));
    await user.click(await screen.findByRole("option", { name: "Last 12 months" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ period: "12m" }));
    expect(within(bar).getByRole("combobox", { name: "Interval" })).toHaveTextContent("By month");
  });
});
