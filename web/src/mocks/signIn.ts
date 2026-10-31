// The local administrator's sign-in in the mock backend (ADR-0030): one
// account, `admin`, whose password must be changed first; from the third
// failure in a row, attempts wait (429 with Retry-After).
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";

type S = components["schemas"];

/** The install password the mock's administrator starts with. */
export const MOCK_ADMIN_PASSWORD = "install-password";

const state = { password: MOCK_ADMIN_PASSWORD, mustChange: true, failures: 0, lockedUntil: 0 };

export function resetSignIn() {
  Object.assign(state, { password: MOCK_ADMIN_PASSWORD, mustChange: true, failures: 0, lockedUntil: 0 });
}

function problem(
  status: number,
  slug: string,
  title: string,
  detail: string,
  extra: Partial<S["Problem"]> = {},
  headers = {},
) {
  return HttpResponse.json(
    { type: `https://rvav.dev/problems/${slug}`, title, status, detail, request_id: "rq-signin", ...extra },
    { status, headers: { "Content-Type": "application/problem+json", ...headers } },
  );
}

function failed() {
  state.failures++;
  if (state.failures >= 3) state.lockedUntil = Date.now() + 1000 * 2 ** (state.failures - 3);
}

function waiting() {
  const left = Math.ceil((state.lockedUntil - Date.now()) / 1000);
  return left > 0
    ? problem(
        429,
        "too-many-attempts",
        "Too many failed attempts",
        `Try again in ${left} seconds.`,
        {},
        {
          "Retry-After": String(left),
        },
      )
    : null;
}

export function signInHandlers(base: string, wait: () => Promise<void>) {
  return [
    http.post(`${base}/auth/sign-in`, async ({ request }) => {
      await wait();
      const body = (await request.json()) as S["SignInRequest"];
      const locked = waiting();
      if (locked) return locked;
      if (body.username.trim().toLowerCase() !== "admin" || body.password !== state.password) {
        failed();
        return problem(401, "unauthenticated", "Sign-in failed", "The username or password is wrong.");
      }
      state.failures = 0;
      return HttpResponse.json<S["SignInResult"]>({ password_change_required: state.mustChange });
    }),
    http.post(`${base}/auth/password`, async ({ request }) => {
      await wait();
      const body = (await request.json()) as S["PasswordChange"];
      const locked = waiting();
      if (locked) return locked;
      const field = (f: string, code: string, message: string) =>
        problem(422, "validation", "Some inputs need attention", message, { errors: [{ field: f, code, message }] });
      if (body.current_password !== state.password) {
        failed();
        return field("/current_password", "wrong", "The current password is wrong.");
      }
      if (body.new_password.length < 12) return field("/new_password", "too_short", "Use at least 12 characters.");
      if (body.new_password === body.current_password)
        return field("/new_password", "unchanged", "Choose a password other than the current one.");
      if (body.new_password.trim().toLowerCase() === "admin")
        return field("/new_password", "username", "Choose a password other than the username.");
      Object.assign(state, { password: body.new_password, mustChange: false, failures: 0, lockedUntil: 0 });
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/auth/sign-out`, () => new HttpResponse(null, { status: 204 })),
  ];
}
