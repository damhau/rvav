import { useState } from "react";

/**
 * What a dialog gives the focus back to: the element that had it as the
 * dialog opened, or, for an item of a dropdown menu (which is gone by the time
 * the dialog closes), that menu's button.
 */
export function openerOf(el: Element | null): HTMLElement | null {
  const menu = el?.closest('[role="menu"]');
  if (menu?.id) {
    const trigger = [...document.querySelectorAll<HTMLElement>("[aria-controls]")].find(
      (t) => t.getAttribute("aria-controls") === menu.id,
    );
    if (trigger) return trigger;
  }
  return el instanceof HTMLElement && el !== document.body ? el : null;
}

/**
 * The opener, read on the first render of a dialog's content: Radix gives the
 * focus back to a Dialog.Trigger only, and by the time its open-autofocus
 * runs, a menu item that opened the dialog has left the page.
 */
export function useOpener() {
  const [opener] = useState(() => openerOf(document.activeElement));
  return (e: Event) => {
    if (!e.defaultPrevented && opener?.isConnected) {
      e.preventDefault();
      opener.focus();
    }
  };
}
