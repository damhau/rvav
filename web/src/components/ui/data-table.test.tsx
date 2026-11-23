import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./confirm-dialog";
import { MenuItem, MenuLabel } from "./dropdown-menu";
import { DataTable, PHONE, TableSearch, type Column } from "./data-table";
import { EmptyState } from "./empty-state";

interface Vm {
  name: string;
  cpus: number;
}

const vms: Vm[] = [
  { name: "web-02", cpus: 4 },
  { name: "web-10", cpus: 2 },
  { name: "db-01", cpus: 8 },
];

const columns: Column<Vm>[] = [
  { id: "name", header: "Name", cell: (v) => <a href={`#${v.name}`}>{v.name}</a>, sortValue: (v) => v.name },
  { id: "cpus", header: "vCPU", cell: (v) => v.cpus, sortValue: (v) => v.cpus, align: "end" },
];

const names = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

describe("DataTable", () => {
  it("sorts by a column: ascending, descending, then as given", async () => {
    const user = userEvent.setup();
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} />);
    expect(screen.getByRole("table", { name: "VMs" })).toBeInTheDocument();
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);

    const header = screen.getByRole("columnheader", { name: "Name" });
    await user.click(within(header).getByRole("button", { name: "Name" }));
    expect(header).toHaveAttribute("aria-sort", "ascending");
    expect(names()).toEqual(["db-01", "web-02", "web-10"]);

    await user.click(within(header).getByRole("button", { name: "Name" }));
    expect(header).toHaveAttribute("aria-sort", "descending");
    expect(names()).toEqual(["web-10", "web-02", "db-01"]);

    await user.click(within(header).getByRole("button", { name: "Name" }));
    expect(header).not.toHaveAttribute("aria-sort");
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);
  });

  it("starts from a default sort, numbers as numbers", () => {
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        defaultSort={{ id: "cpus", desc: true }}
      />,
    );
    expect(names()).toEqual(["db-01", "web-02", "web-10"]);
  });

  it("sorts the rows without a value last, either way", async () => {
    const user = userEvent.setup();
    const used: Column<Vm & { used?: number }> = {
      id: "used",
      header: "Last used",
      cell: (v) => v.used ?? "never",
      sortValue: (v) => v.used,
    };
    render(
      <DataTable
        label="VMs"
        columns={[columns[0]!, used]}
        rows={[{ ...vms[0]!, used: 2 }, vms[1]!, { ...vms[2]!, used: 1 }]}
        rowKey={(v) => v.name}
      />,
    );
    const header = screen.getByRole("columnheader", { name: "Last used" });
    await user.click(within(header).getByRole("button", { name: "Last used" }));
    expect(names()).toEqual(["db-01", "web-02", "web-10"]);
    await user.click(within(header).getByRole("button", { name: "Last used" }));
    expect(names()).toEqual(["web-02", "db-01", "web-10"]);
  });

  it("leaves rows the server sorted in their order, a header only asking for another sort", async () => {
    const user = userEvent.setup();
    const onSortChange = vi.fn();
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        sort={{ id: "cpus", desc: true }}
        onSortChange={onSortChange}
        presorted
      />,
    );
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);
    expect(screen.getByRole("columnheader", { name: "vCPU" })).toHaveAttribute("aria-sort", "descending");
    await user.click(screen.getByRole("button", { name: "Name" }));
    expect(onSortChange).toHaveBeenCalledWith({ id: "name", desc: false });
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);
  });

  it("gives each row a named menu, and none to a row without items", async () => {
    const user = userEvent.setup();
    const remove = vi.fn<(name: string) => void>();
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        rowMenuLabel={(v) => `Actions for ${v.name}`}
        rowMenu={(v) =>
          v.name === "db-01" ? null : (
            <MenuItem danger onSelect={() => remove(v.name)}>
              Remove
            </MenuItem>
          )
        }
      />,
    );
    expect(screen.queryByRole("button", { name: "Actions for db-01" })).toBeNull();
    expect(screen.getByRole("columnheader", { name: "Actions" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Actions for web-10" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    expect(remove).toHaveBeenCalledWith("web-10");
  });

  it("hands the focus to the row at its place when a row's menu removes it, or to the last row", async () => {
    const user = userEvent.setup();
    function Removing() {
      const [rows, setRows] = useState(vms);
      return (
        <DataTable
          label="VMs"
          columns={columns}
          rows={rows}
          rowKey={(v) => v.name}
          rowMenuLabel={(v) => `Actions for ${v.name}`}
          rowMenu={(v) => (
            <MenuItem danger onSelect={() => setRows((r) => r.filter((x) => x !== v))}>
              Remove
            </MenuItem>
          )}
        />
      );
    }
    render(<Removing />);
    await user.click(screen.getByRole("button", { name: "Actions for web-10" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "Actions for db-01" })).toHaveFocus());
    await user.keyboard("{Enter}");
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "Actions for web-02" })).toHaveFocus());
  });

  it("hands the focus to the row at its place when a dialog a row opened removes it", async () => {
    const user = userEvent.setup();
    function Asking() {
      const [rows, setRows] = useState(vms);
      const [asking, setAsking] = useState<Vm | null>(null);
      return (
        <>
          <DataTable
            label="VMs"
            columns={columns}
            rows={rows}
            rowKey={(v) => v.name}
            rowMenuLabel={(v) => `Actions for ${v.name}`}
            rowMenu={(v) => <MenuItem onSelect={() => setAsking(v)}>Remove</MenuItem>}
          />
          {asking && (
            <ConfirmDialog
              open
              onOpenChange={(o) => !o && setAsking(null)}
              title={`Remove ${asking.name}?`}
              description="It goes."
              confirmLabel="Remove VM"
              onConfirm={() => {
                setRows((r) => r.filter((x) => x !== asking));
                setAsking(null);
              }}
            />
          )}
        </>
      );
    }
    render(<Asking />);
    await user.click(screen.getByRole("button", { name: "Actions for web-02" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(await screen.findByRole("button", { name: "Remove VM" }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "Actions for web-10" })).toHaveFocus());
  });

  it("follows a row's link from anywhere in the row", async () => {
    const user = userEvent.setup();
    const followed = vi.fn((e: Event) => e.preventDefault());
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} rowLinks />);
    screen.getByRole("link", { name: "web-10" }).addEventListener("click", followed);
    await user.click(screen.getByRole("cell", { name: "2" }));
    expect(followed).toHaveBeenCalledOnce();
    expect(screen.getByRole("link", { name: "web-10" })).toHaveFocus();
  });

  it("doesn't follow the link for a click inside the row's menu", async () => {
    const user = userEvent.setup();
    const followed = vi.fn((e: Event) => e.preventDefault());
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        rowLinks
        rowMenuLabel={(v) => `Actions for ${v.name}`}
        rowMenu={() => (
          <>
            <MenuLabel>Loading…</MenuLabel>
            <MenuItem>Remove</MenuItem>
          </>
        )}
      />,
    );
    screen.getByRole("link", { name: "web-10" }).addEventListener("click", followed);
    await user.click(screen.getByRole("button", { name: "Actions for web-10" }));
    await user.click(await screen.findByText("Loading…"));
    expect(followed).not.toHaveBeenCalled();
  });

  it("lights up only rows that open under the pointer", () => {
    const { rerender } = render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} />);
    expect(screen.getAllByRole("row")[1]).not.toHaveClass("hover:bg-sunken/60");
    rerender(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} rowLinks />);
    expect(screen.getAllByRole("row")[1]).toHaveClass("hover:bg-sunken/60");
  });

  it("selects rows one by one or all at once", async () => {
    const user = userEvent.setup();
    function Selecting() {
      const [selected, setSelected] = useState<Set<string>>(new Set());
      return (
        <>
          <DataTable
            label="VMs"
            columns={columns}
            rows={vms}
            rowKey={(v) => v.name}
            selection={{ selected, onChange: setSelected, label: (v) => `Select ${v.name}` }}
          />
          <output>{[...selected].sort().join(",")}</output>
        </>
      );
    }
    render(<Selecting />);
    await user.click(screen.getByRole("checkbox", { name: "Select web-10" }));
    expect(screen.getByRole("status")).toHaveTextContent("web-10");
    await user.click(screen.getByRole("checkbox", { name: "Select all" }));
    expect(screen.getByRole("status")).toHaveTextContent("db-01,web-02,web-10");
    expect(screen.getByRole("checkbox", { name: "Select all" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "Select all" }));
    expect(screen.getByRole("status")).toHaveTextContent("");
  });

  it("leaves rows selected but not shown as they are", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms.slice(0, 2)}
        rowKey={(v) => v.name}
        selection={{
          selected: new Set(["hidden-01"]),
          onChange,
          label: (v) => `Select ${v.name}`,
          allLabel: "Select all shown",
        }}
      />,
    );
    await user.click(screen.getByRole("checkbox", { name: "Select all shown" }));
    const all = onChange.mock.calls[0]![0] as Set<string>;
    expect([...all].sort()).toEqual(["hidden-01", ...vms.slice(0, 2).map((v) => v.name)].sort());
  });

  it("says it is loading, keeping the headers", () => {
    render(<DataTable label="VMs" columns={columns} rows={undefined} rowKey={(v) => v.name} loading />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading");
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeInTheDocument();
  });

  it("shows its empty state, with the next step, and its toolbar", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={[]}
        rowKey={(v) => v.name}
        toolbar={<TableSearch label="Filter VMs" value="" onChange={onChange} />}
        empty={
          <EmptyState title="No VMs" body="Request one from the catalog." action={<button>Request a VM</button>} />
        }
      />,
    );
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByText("No VMs")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request a VM" })).toBeInTheDocument();
    await user.type(screen.getByRole("searchbox", { name: "Filter VMs" }), "w");
    expect(onChange).toHaveBeenCalledWith("w");
  });

  it("puts rows under their group's heading, in the order the groups come, sorted within each", async () => {
    const user = userEvent.setup();
    const rows = [...vms, { name: "db-00", cpus: 1 }];
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={rows}
        rowKey={(v) => v.name}
        groups={{ by: (v) => v.name.slice(0, -3), label: (key, list) => `${key} · ${list.length} VMs` }}
      />,
    );
    const web = screen.getByRole("rowheader", { name: "web · 2 VMs" });
    expect(web).toHaveAttribute("scope", "rowgroup");
    expect(web).toHaveAttribute("colspan", "2");
    expect(screen.getByRole("rowheader", { name: "db · 2 VMs" })).toBeInTheDocument();
    const order = () => screen.getAllByRole("row").map((r) => r.textContent);
    expect(order().slice(1)).toEqual(["web · 2 VMs", "web-024", "web-102", "db · 2 VMs", "db-018", "db-001"]);

    const header = screen.getByRole("columnheader", { name: "vCPU" });
    await user.click(within(header).getByRole("button", { name: "vCPU" }));
    expect(order().slice(1)).toEqual(["web · 2 VMs", "web-102", "web-024", "db · 2 VMs", "db-001", "db-018"]);
  });

  it("gives rows without a group no heading", () => {
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        groups={{ by: () => undefined, label: () => "None" }}
      />,
    );
    expect(screen.queryByRole("rowheader")).toBeNull();
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);
  });

  it("hands the focus to the row at its place across groups when a row's menu removes it", async () => {
    const user = userEvent.setup();
    function Removing() {
      const [rows, setRows] = useState([...vms, { name: "db-03", cpus: 2 }]);
      return (
        <DataTable
          label="VMs"
          columns={columns}
          rows={rows}
          rowKey={(v) => v.name}
          groups={{ by: (v) => v.name.slice(0, -3), label: (key) => key }}
          rowMenuLabel={(v) => `Actions for ${v.name}`}
          rowMenu={(v) => (
            <MenuItem danger onSelect={() => setRows((r) => r.filter((x) => x !== v))}>
              Remove
            </MenuItem>
          )}
        />
      );
    }
    render(<Removing />);
    await user.click(screen.getByRole("button", { name: "Actions for web-10" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "Actions for db-01" })).toHaveFocus());
  });

  it("names a column of buttons for screen readers only", () => {
    const remove: Column<Vm> = {
      id: "remove",
      header: "Remove",
      hideHeader: true,
      cell: (v) => <button type="button">Remove {v.name}</button>,
    };
    render(<DataTable label="VMs" columns={[...columns, remove]} rows={vms} rowKey={(v) => v.name} />);
    expect(screen.getByRole("columnheader", { name: "Remove" }).firstElementChild).toHaveClass("sr-only");
  });

  it("shows what failed instead of the rows", () => {
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={undefined}
        rowKey={(v) => v.name}
        error={<p role="alert">The list failed</p>}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("The list failed");
    expect(screen.queryByRole("table")).toBeNull();
  });
});

describe("DataTable on a phone", () => {
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

  it("stacks each row as a card: the first column its title, the others labelled", () => {
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} />);
    const table = screen.getByRole("table", { name: "VMs" });
    expect(table).toHaveAttribute("data-stacked");
    expect(screen.getByRole("columnheader", { name: "vCPU" })).toBeInTheDocument();
    const [title, cpus] = within(screen.getAllByRole("row")[2]!).getAllByRole("cell");
    expect(title).toHaveTextContent(/^web-10$/);
    expect(within(cpus!).getByText("vCPU")).toHaveAttribute("aria-hidden", "true");
    expect(cpus).toHaveTextContent("vCPU2");
  });

  it("sorts from the strip above the cards", async () => {
    const user = userEvent.setup();
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} />);
    expect(screen.getByText("Sort by")).toHaveAttribute("aria-hidden", "true");
    await user.click(screen.getByRole("button", { name: "vCPU" }));
    expect(screen.getByRole("columnheader", { name: "vCPU" })).toHaveAttribute("aria-sort", "ascending");
    expect(names()).toEqual(["web-10", "web-02", "db-01"]);
  });

  it("names select all in words, and keeps each card's checkbox and menu", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const remove = vi.fn<(name: string) => void>();
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        selection={{ selected: new Set(), onChange, label: (v) => `Select ${v.name}` }}
        rowMenuLabel={(v) => `Actions for ${v.name}`}
        rowMenu={(v) => <MenuItem onSelect={() => remove(v.name)}>Remove</MenuItem>}
      />,
    );
    await user.click(screen.getByText("Select all"));
    expect([...(onChange.mock.calls[0]![0] as Set<string>)].sort()).toEqual(["db-01", "web-02", "web-10"]);
    await user.click(screen.getByRole("checkbox", { name: "Select db-01" }));
    expect([...(onChange.mock.calls[1]![0] as Set<string>)]).toEqual(["db-01"]);
    await user.click(screen.getByRole("button", { name: "Actions for web-02" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    expect(remove).toHaveBeenCalledWith("web-02");
  });

  it("follows a card's link from anywhere in the card", async () => {
    const user = userEvent.setup();
    const followed = vi.fn((e: Event) => e.preventDefault());
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} rowLinks />);
    screen.getByRole("link", { name: "web-10" }).addEventListener("click", followed);
    await user.click(within(screen.getAllByRole("row")[2]!).getByText("vCPU"));
    expect(followed).toHaveBeenCalledOnce();
  });

  it("shows its empty state as on a desktop", () => {
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={[]}
        rowKey={(v) => v.name}
        empty={<EmptyState title="No VMs" body="Request one." />}
      />,
    );
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByText("No VMs")).toBeInTheDocument();
  });

  it("stacks a group's heading above its cards", () => {
    render(
      <DataTable
        label="VMs"
        columns={columns}
        rows={vms}
        rowKey={(v) => v.name}
        groups={{ by: (v) => v.name.slice(0, -3), label: (key) => key }}
      />,
    );
    const heading = screen.getByRole("rowheader", { name: "db" });
    expect(heading.closest("[role=rowgroup]")).toHaveTextContent(/^dbdb-01vCPU8$/);
  });

  it("leaves a column's hidden header off its cards", () => {
    const remove: Column<Vm> = {
      id: "remove",
      header: "Remove",
      hideHeader: true,
      cell: (v) => <button type="button">Remove {v.name}</button>,
    };
    render(<DataTable label="VMs" columns={[...columns, remove]} rows={vms} rowKey={(v) => v.name} />);
    const cells = within(screen.getAllByRole("row")[1]!).getAllByRole("cell");
    expect(cells[2]).toHaveTextContent(/^Remove web-02$/);
    expect(within(cells[2]!).queryByText("Remove", { exact: true })).toBeNull();
  });

  it("keeps the columns side by side when asked not to stack", () => {
    render(<DataTable label="VMs" columns={columns} rows={vms} rowKey={(v) => v.name} stack={false} />);
    expect(screen.getByRole("table", { name: "VMs" })).not.toHaveAttribute("data-stacked");
    expect(screen.queryByText("Sort by")).toBeNull();
    expect(names()).toEqual(["web-02", "web-10", "db-01"]);
  });
});
