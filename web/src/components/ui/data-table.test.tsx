import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { MenuItem, MenuLabel } from "./dropdown-menu";
import { DataTable, TableSearch, type Column } from "./data-table";
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
