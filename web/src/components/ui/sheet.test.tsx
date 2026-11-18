import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Button } from "./button";
import { ConfirmDialog } from "./confirm-dialog";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "./dropdown-menu";
import { Field, Input } from "./input";
import { Sheet } from "./sheet";

function Editing({ onSave }: { onSave: (name: string) => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  return (
    <>
      <Button onClick={() => setOpen(true)}>New size</Button>
      <Menu>
        <MenuTrigger asChild>
          <Button aria-label="Actions for Medium">…</Button>
        </MenuTrigger>
        <MenuContent>
          <MenuItem onSelect={() => setOpen(true)}>Change</MenuItem>
        </MenuContent>
      </Menu>
      <Sheet
        open={open}
        onOpenChange={setOpen}
        title="New size"
        description="Size pickers offer it to the projects you choose."
        submitLabel="Save size"
        submitDisabled={!name}
        onSubmit={() => {
          onSave(name);
          setOpen(false);
        }}
      >
        <Field label="Label" htmlFor="size-label">
          <Input id="size-label" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
      </Sheet>
    </>
  );
}

describe("Sheet", () => {
  it("is a named dialog whose footer saves the form", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<Editing onSave={onSave} />);
    await user.click(screen.getByRole("button", { name: "New size" }));
    const sheet = screen.getByRole("dialog", { name: "New size" });
    expect(sheet).toHaveAccessibleDescription("Size pickers offer it to the projects you choose.");
    expect(within(sheet).getByRole("textbox", { name: "Label" })).toHaveFocus();
    expect(within(sheet).getByRole("button", { name: "Save size" })).toBeDisabled();
    await user.type(within(sheet).getByRole("textbox", { name: "Label" }), "Large");
    await user.click(within(sheet).getByRole("button", { name: "Save size" }));
    expect(onSave).toHaveBeenCalledWith("Large");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps the focus inside, closes with Escape and gives the focus back", async () => {
    const user = userEvent.setup();
    render(<Editing onSave={() => {}} />);
    const opener = screen.getByRole("button", { name: "New size" });
    await user.click(opener);
    const sheet = screen.getByRole("dialog", { name: "New size" });
    for (let i = 0; i < 6; i++) {
      await user.tab();
      expect(sheet).toContainElement(document.activeElement as HTMLElement);
    }
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it("closes with Cancel without saving", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<Editing onSave={onSave} />);
    await user.click(screen.getByRole("button", { name: "New size" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("gives the focus back to the menu button when a menu item opened it", async () => {
    const user = userEvent.setup();
    render(<Editing onSave={() => {}} />);
    const menu = screen.getByRole("button", { name: "Actions for Medium" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    await screen.findByRole("dialog", { name: "New size" });
    await user.keyboard("{Escape}");
    await waitFor(() => expect(menu).toHaveFocus());
  });
});

function Removing({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(true);
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      title="Remove RHEL 9.4?"
      description="Template pickers stop offering rhel9."
      consequences="Offered to all projects. VMs already built from it keep running."
      confirmLabel="Remove template"
      onConfirm={() => {
        onConfirm();
        setOpen(false);
      }}
    />
  );
}

describe("ConfirmDialog", () => {
  it("says what goes and what depends on it before removing", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<Removing onConfirm={onConfirm} />);
    const dialog = screen.getByRole("alertdialog", { name: "Remove RHEL 9.4?" });
    expect(dialog).toHaveAccessibleDescription("Template pickers stop offering rhel9.");
    expect(dialog).toHaveTextContent("VMs already built from it keep running.");
    await user.click(within(dialog).getByRole("button", { name: "Remove template" }));
    expect(onConfirm).toHaveBeenCalledOnce();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("does nothing on Cancel", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<Removing onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
