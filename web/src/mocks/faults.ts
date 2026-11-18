// Failures a test asks the mock API for. Browser specs reach them through
// `window.__rvavMocks` (mocks/browser.ts), since the handlers run in the page.

/** How many more `forms/values` calls fail with a 500, by field. */
const fieldValues = new Map<string, number>();

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
  reset() {
    fieldValues.clear();
  },
};
