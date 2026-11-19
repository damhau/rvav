import { useState } from "react";

const focusable = [
  "input:not([type=hidden]):not([disabled])",
  "textarea:not([disabled])",
  "select:not([disabled])",
  "button:not([disabled])",
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
 * The opener, read on the first render of a dialog's content: Radix gives the
 * focus back to a Dialog.Trigger only, and by the time its open-autofocus
 * runs, a menu item that opened the dialog has left the page. `instead` names
 * another element for when the dialog's action removes its opener.
 */
export function useOpener(instead?: () => HTMLElement | null | undefined) {
  const [opener] = useState(() => openerOf(document.activeElement));
  return (e: Event) => {
    if (e.defaultPrevented) return;
    const target = instead?.() ?? (opener?.isConnected ? opener : null);
    if (target) {
      e.preventDefault();
      target.focus();
    }
  };
}
