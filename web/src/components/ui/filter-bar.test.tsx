import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { optBool, optString } from "@/routes/-search";
import { PHONE } from "./data-table";
import {
  FilterBar,
  FilterCheck,
  FilterEmpty,
  FilterInput,
  FilterSelect,
  useAddressFilters,
  type Filters,
} from "./filter-bar";

interface ListSearch {
  q?: string;
  status?: string;
  mine?: boolean;
  owner?: string;
  from?: string;
  folder?: string;
  tab?: string;
}

const keys = ["q", "status", "mine", "owner", "from", "folder"] as const;

function List() {
  const filters = useAddressFilters<ListSearch>(list.useSearch(), keys);
  return (
    <>
      <FilterBar filters={filters} search={{ label: "Search requests", placeholder: "Name or number" }}>
        <FilterSelect
          name="status"
          label="Status"
          any="All statuses"
          options={[
            { value: "open", label: "Open" },
            { value: "failed", label: "Failed" },
          ]}
        />
        <FilterCheck name="mine" label="Only mine" />
        <FilterCheck name="owner" value="me" label="Owned by me" />
        <FilterInput name="from" type="date" label="From" />
        <FilterInput name="folder" label="Folder" />
      </FilterBar>
      {filters.filtered && <FilterEmpty filters={filters} title="No requests match" body="Try other words." />}
    </>
  );
}

const root = createRootRoute({ component: Outlet });
const list = createRoute({
  getParentRoute: () => root,
  path: "/list",
  validateSearch: (s: Record<string, unknown>): ListSearch => ({
    q: optString(s.q),
    status: optString(s.status),
    mine: optBool(s.mine),
    owner: optString(s.owner),
    from: optString(s.from),
    folder: optString(s.folder),
    tab: optString(s.tab),
  }),
  component: List,
});

function renderList(path: string) {
  const router = createRouter({
    routeTree: root.addChildren([list]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(<RouterProvider router={router as never} />);
  return router;
}

const search = (router: ReturnType<typeof renderList>) => router.state.location.search as Record<string, unknown>;
const barClear = () =>
  within(screen.getByRole("group", { name: "Filters" })).getByRole("button", { name: "Clear filters" });

describe("FilterBar", () => {
  it("keeps the search, a select, a check and a date in the address, beside the keys it doesn't own", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?tab=runs");
    const box = await screen.findByRole("searchbox", { name: "Search requests" });
    expect(screen.getByRole("group", { name: "Filters" })).toContainElement(box);
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();

    await user.type(box, "  web ");
    await waitFor(() => expect(search(router)).toEqual({ tab: "runs", q: "web" }));

    await user.click(screen.getByRole("combobox", { name: "Status" }));
    await user.click(await screen.findByRole("option", { name: "Failed" }));
    await waitFor(() => expect(search(router)).toEqual({ tab: "runs", q: "web", status: "failed" }));
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("Failed");

    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await user.click(screen.getByRole("checkbox", { name: "Owned by me" }));
    await waitFor(() => expect(search(router)).toMatchObject({ mine: true, owner: "me" }));

    await user.type(screen.getByLabelText("From"), "2026-11-02");
    await waitFor(() => expect(search(router)).toMatchObject({ from: "2026-11-02" }));

    await user.click(screen.getByRole("combobox", { name: "Status" }));
    await user.click(await screen.findByRole("option", { name: "All statuses" }));
    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await waitFor(() => expect(search(router)).toEqual({ tab: "runs", q: "web", owner: "me", from: "2026-11-02" }));
  });

  it("opens filtered from a link, showing a value no option offers as itself", async () => {
    renderList("/list?q=edge&status=gone&folder=Prod%2FWeb&mine=true");
    expect(await screen.findByRole("searchbox", { name: "Search requests" })).toHaveValue("edge");
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("gone");
    expect(screen.getByRole("checkbox", { name: "Only mine" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Owned by me" })).not.toBeChecked();
    expect(screen.getByRole("textbox", { name: "Folder" })).toHaveValue("Prod/Web");
    expect(barClear()).toBeInTheDocument();
  });

  it("clears every filter, keeps the tab, and gives the focus to the search box", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?tab=runs&q=edge&status=failed&from=2026-11-02");
    const box = await screen.findByRole("searchbox", { name: "Search requests" });

    await user.click(barClear());
    await waitFor(() => expect(search(router)).toEqual({ tab: "runs" }));
    expect(box).toHaveValue("");
    expect(box).toHaveFocus();
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("All statuses");
    expect(screen.getByLabelText("From")).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("drops what is typed and not yet in the address when the filters are cleared", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?status=failed");
    const box = await screen.findByRole("searchbox", { name: "Search requests" });
    await user.type(box, "ab");
    await user.type(screen.getByRole("textbox", { name: "Folder" }), "Prod");
    await user.click(barClear());
    await act(() => new Promise((r) => setTimeout(r, 400)));
    expect(search(router)).toEqual({});
    expect(box).toHaveValue("");
    expect(screen.getByRole("textbox", { name: "Folder" })).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("refuses a filter named for a key the filters don't own", () => {
    const filters: Filters<ListSearch> = {
      values: {},
      set: () => undefined,
      filtered: false,
      count: 0,
      clear: () => undefined,
      cleared: 0,
      keys: ["status"],
      bar: { current: null },
    };
    const quiet = vi.spyOn(console, "error").mockImplementation(() => undefined);
    expect(() =>
      render(
        <FilterBar filters={filters}>
          <FilterCheck name="mine" label="Only mine" />
        </FilterBar>,
      ),
    ).toThrow("The filter mine is none of the filters' keys: status");
    quiet.mockRestore();
  });

  it("clears from the list's empty state too", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?status=open");
    await screen.findByText("No requests match");
    const [, empty] = screen.getAllByRole("button", { name: "Clear filters" });
    await user.click(empty!);
    await waitFor(() => expect(search(router)).toEqual({}));
    expect(screen.queryByText("No requests match")).toBeNull();
    expect(screen.getByRole("searchbox", { name: "Search requests" })).toHaveFocus();
  });

  it("is worked from the keyboard: the search, each filter in order, then Clear", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?folder=Prod");
    await screen.findByRole("searchbox", { name: "Search requests" });

    await user.tab();
    expect(screen.getByRole("searchbox", { name: "Search requests" })).toHaveFocus();
    await user.keyboard("db");
    await user.tab();
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("checkbox", { name: "Only mine" })).toHaveFocus();
    await user.keyboard(" ");
    await waitFor(() => expect(search(router)).toMatchObject({ mine: true }));
    await user.tab();
    expect(screen.getByRole("checkbox", { name: "Owned by me" })).toHaveFocus();
    await user.tab();
    expect(screen.getByLabelText("From")).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("textbox", { name: "Folder" })).toHaveFocus();
    await user.tab();
    const clear = barClear();
    expect(clear).toHaveFocus();
    await waitFor(() => expect(search(router)).toEqual({ folder: "Prod", mine: true, q: "db" }));

    await user.keyboard("{Enter}");
    await waitFor(() => expect(search(router)).toEqual({}));
    expect(screen.getByRole("searchbox", { name: "Search requests" })).toHaveFocus();
    await act(() => new Promise((r) => setTimeout(r, 300)));
    expect(search(router)).toEqual({});
  });
});

describe("FilterBar on a phone", () => {
  beforeEach(() => {
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query) =>
        ({
          matches: query === PHONE,
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList,
    );
  });
  afterEach(() => vi.restoreAllMocks());

  it("shows the search and a Filters button that says how many are set, and keeps Clear in view", async () => {
    const user = userEvent.setup();
    const router = renderList("/list?q=web&status=failed&mine=true");
    const bar = await screen.findByRole("group", { name: "Filters" });
    expect(within(bar).getByRole("searchbox", { name: "Search requests" })).toHaveValue("web");
    const toggle = within(bar).getByRole("button", { name: "Filters (2)" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(bar).queryByRole("combobox", { name: "Status" })).toBeNull();
    expect(within(bar).queryByRole("checkbox", { name: "Only mine" })).toBeNull();
    expect(barClear()).toBeVisible();

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveFocus();
    expect(within(bar).getByRole("combobox", { name: "Status" })).toHaveTextContent("Failed");
    await user.click(within(bar).getByRole("checkbox", { name: "Only mine" }));
    await waitFor(() => expect(search(router)).toEqual({ q: "web", status: "failed" }));
    expect(toggle).toHaveAccessibleName("Filters (1)");

    await user.click(barClear());
    await waitFor(() => expect(search(router)).toEqual({}));
    expect(toggle).toHaveAccessibleName("Filters");
    expect(within(bar).getByRole("searchbox", { name: "Search requests" })).toHaveFocus();
  });

  it("is worked from the keyboard: the search, Filters, what it shows, then Clear", async () => {
    const user = userEvent.setup();
    renderList("/list?folder=Prod");
    await screen.findByRole("searchbox", { name: "Search requests" });

    await user.tab();
    expect(screen.getByRole("searchbox", { name: "Search requests" })).toHaveFocus();
    await user.tab();
    const toggle = screen.getByRole("button", { name: "Filters (1)" });
    expect(toggle).toHaveFocus();
    await user.tab();
    expect(barClear()).toHaveFocus();

    await user.tab({ shift: true });
    await user.keyboard("{Enter}");
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveFocus();
    await user.tab({ shift: true });
    await user.keyboard(" ");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveFocus();
    expect(screen.queryByRole("textbox", { name: "Folder" })).toBeNull();
  });
});
