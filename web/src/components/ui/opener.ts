import { useEffect, useRef, useState, type RefObject } from "react";

// A Button that turns disabled while focused is aria-disabled until the focus
// leaves it (button.tsx): one is no control to move the focus to.
const focusable = [
  "input:not([type=hidden]):not([disabled])",
  "textarea:not([disabled])",
  "select:not([disabled])",
  "button:not([disabled]):not([aria-disabled=true])",
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

/**
 * Moves the focus to the first control marked invalid in root, or into the
 * first group marked so (a radio group): its note then reads out the problem.
 * False when nothing is invalid.
 */
export function focusInvalid(root: ParentNode | null | undefined): boolean {
  const invalid = root?.querySelector<HTMLElement>('[aria-invalid="true"]');
  const control = invalid?.matches(focusable) ? invalid : invalid?.querySelector<HTMLElement>(focusable);
  control?.focus();
  return Boolean(control);
}

/**
 * Where the focus goes as a form's save ends. Its submit button, disabled
 * while the save runs, drops the focus to the page: a refusal then moves it
 * to the first field marked invalid in root, and any other end to `then` (the
 * submit button, or what stands for it once the save leaves it disabled).
 * Call `submitted` as the form submits, and `interacted` on the person's keys
 * and clicks in it: a field that turns invalid as they type keeps their focus.
 */
export function useSaveFocus(
  root: RefObject<HTMLElement | null>,
  pending: boolean | undefined,
  then: () => HTMLElement | null | undefined,
) {
  // From a submit to the render that shows how it went. A quick answer can come with no render
  // while pending, so a field marked invalid ends the wait too.
  const awaiting = useRef<"submitted" | "saving" | null>(null);
  useEffect(() => {
    if (!awaiting.current) return;
    if (pending) {
      awaiting.current = "saving";
      return;
    }
    if (focusInvalid(root.current)) awaiting.current = null;
    else if (awaiting.current === "saving") {
      awaiting.current = null;
      const active = document.activeElement;
      if (!active || active === document.body || active === root.current?.closest('[role="dialog"]')) then()?.focus();
    }
  });
  return {
    submitted: () => {
      awaiting.current = "submitted";
    },
    interacted: () => {
      awaiting.current = null;
    },
  };
}

/**
 * What a dialog gives the focus back to: the element that had it as the
 * dialog opened, or, for an item of a dropdown menu or of its submenus (which
 * are gone by the time the dialog closes), that menu's button.
 */
export function openerOf(el: Element | null): HTMLElement | null {
  const menu = el?.closest('[role="menu"]');
  if (menu?.id) {
    const trigger = [...document.querySelectorAll<HTMLElement>("[aria-controls]")].find(
      (t) => t.getAttribute("aria-controls") === menu.id,
    );
    // A submenu's trigger is an item of its parent menu, gone as well: the menu's button stays.
    if (trigger) return trigger.closest('[role="menu"]') ? openerOf(trigger) : trigger;
  }
  return el instanceof HTMLElement && el !== document.body ? el : null;
}

/**
 * What stands in for a control of a table's row once its row went: the
 * same control of the row now at its place, else of the last row, else the
 * table (a DataTable's wrapper) itself.
 */
export function rowStandIn(table: HTMLElement | null | undefined, index: number): HTMLElement | null {
  if (!table?.isConnected) return null;
  const rows = [...table.querySelectorAll("tbody tr")];
  for (const row of [rows[index], rows.at(-1)]) {
    const control =
      row?.querySelector<HTMLElement>("[aria-haspopup=menu]") ??
      row?.querySelector<HTMLElement>(`${focusable}, a[href]`);
    if (control) return control;
  }
  table.tabIndex = -1;
  return table;
}

/** The DataTable and row index of a control in a row, for `rowStandIn`. */
function rowOf(el: HTMLElement | null) {
  const row = el?.closest("tr");
  const table = row?.closest<HTMLElement>("[data-table]");
  return row && table ? { table, index: [...row.parentElement!.children].indexOf(row) } : null;
}

/**
 * The opener, read on the first render of a dialog's content: Radix gives the
 * focus back to a Dialog.Trigger only, and by the time its open-autofocus
 * runs, a menu item that opened the dialog has left the page. `instead` names
 * another element for when the dialog's action removes its opener; a row's
 * menu button that went hands the focus to the row at its place.
 */
export function useOpener(instead?: () => HTMLElement | null | undefined) {
  const [opener] = useState(() => openerOf(document.activeElement));
  const [row] = useState(() => rowOf(opener));
  return (e: Event) => {
    if (e.defaultPrevented) return;
    const target = instead?.() ?? (opener?.isConnected ? opener : row ? rowStandIn(row.table, row.index) : null);
    if (target) {
      e.preventDefault();
      target.focus();
    }
  };
}

/**
 * Once `settled` (a list refetched after an action), a focus that fell to the
 * page, what had it having gone with its list, goes to `then`.
 */
export async function catchFocus(settled: Promise<unknown>, then: () => HTMLElement | null | undefined) {
  await settled.catch(() => undefined);
  // The list's new rows are drawn a task later.
  await new Promise((r) => setTimeout(r));
  requestAnimationFrame(() => {
    const now = document.activeElement;
    if (!now || now === document.body) then()?.focus();
  });
}
