// Failures a test asks the mock API for. Browser specs reach them through
// `window.__rvavMocks` (mocks/browser.ts), since the handlers run in the page.

import type { components } from "@/lib/api/schema";

type FieldError = components["schemas"]["FieldError"];

/** How many more `forms/values` calls fail with a 500, by field. */
const fieldValues = new Map<string, number>();
/** What the next update of a deployment is refused for, as the server refuses one that changed since Review. */
let updateRefusal: FieldError[] | undefined;

export const faults = {
  failFieldValues(field: string, times: number) {
    fieldValues.set(field, times);
  },
  /** Whether this call for the field fails, counting it. */
  fieldValuesFail(field: string): boolean {
    const left = fieldValues.get(field) ?? 0;
    if (left > 0) fieldValues.set(field, left - 1);
    return left > 0;
  },
  refuseNextUpdate(errors: FieldError[]) {
    updateRefusal = errors;
  },
  /** The refusal for this update, once. */
  updateRefused(): FieldError[] | undefined {
    const errors = updateRefusal;
    updateRefusal = undefined;
    return errors;
  },
  reset() {
    fieldValues.clear();
    updateRefusal = undefined;
  },
};
