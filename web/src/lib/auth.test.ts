// Signing in outside mock mode (ADR-0030): the sign-in page stays reachable
// when the identity provider is down, and says why it is shown. rvav is told
// how each sign-in went, and of each sign-out, for its audit log (ADR-0058).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./env", () => ({
  env: { authMode: "oidc", oidcAuthority: "", oidcClientId: "", oidcScope: "" },
  isMock: false,
}));

const signinRedirect = vi.fn();
const signinRedirectCallback = vi.fn();
const signoutRedirect = vi.fn();
let metadataDown = false;
let signedIn: { access_token: string; expired: boolean } | null = null;
vi.mock("oidc-client-ts", () => ({
  InMemoryWebStorage: class {},
  WebStorageStateStore: class {},
  UserManager: class {
    // The tokens are in memory only: none after a page load.
    getUser = () => (metadataDown ? Promise.reject(new TypeError("Failed to fetch")) : Promise.resolve(signedIn));
    signinRedirect = signinRedirect;
    signinRedirectCallback = signinRedirectCallback;
    signoutRedirect = signoutRedirect;
  },
}));

const options = { provider: { authority: "https://sso.example/realms/rvav", client_id: "rvav", scope: "openid" } };

let fetched: { url: string; init?: RequestInit }[] = [];

function api(optionsStatus: number | "down") {
  fetched = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init?: RequestInit) => {
      fetched.push({ url, init });
      if (url.endsWith("/auth/options")) {
        if (optionsStatus === "down") return Promise.reject(new TypeError("Failed to fetch"));
        return Promise.resolve(
          new Response(optionsStatus === 200 ? JSON.stringify(options) : "bad gateway", { status: optionsStatus }),
        );
      }
      if (url.includes("/auth/sign-")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(new Response("{}", { status: 401 })); // /me: no local session
    }),
  );
}

/** The calls that told rvav of a sign-in or a sign-out. */
const told = () =>
  fetched
    .filter((f) => f.url.includes("/auth/sign-"))
    .map((f) => ({
      url: f.url,
      authorization: (f.init?.headers as Record<string, string>).Authorization,
      body: f.init?.body ? (JSON.parse(f.init.body as string) as unknown) : undefined,
    }));

const replaceState = vi.fn();

function at(pathname: string, search = "") {
  vi.stubGlobal("location", { ...globalThis.location, pathname, search, origin: "https://rvav.example" });
  vi.stubGlobal("history", { replaceState });
}

async function load() {
  vi.resetModules();
  return import("./auth");
}

beforeEach(() => {
  signinRedirect.mockReset().mockResolvedValue(undefined);
  signinRedirectCallback.mockReset();
  signoutRedirect.mockReset().mockResolvedValue(undefined);
  metadataDown = false;
  signedIn = null;
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("signing in with the provider remembered", () => {
  beforeEach(() => localStorage.setItem("rvav.signInMethod", "provider"));

  it("goes straight to the provider from any page", async () => {
    api(200);
    at("/deployments");
    const auth = await load();
    expect(await auth.initAuth()).toBe("redirecting");
    expect(signinRedirect).toHaveBeenCalledWith({ state: "/deployments" });
  });

  it("stays on /sign-in, the way in when the provider is down", async () => {
    api(200);
    at("/sign-in");
    const auth = await load();
    expect(await auth.initAuth()).toBe("sign-in");
    expect(signinRedirect).not.toHaveBeenCalled();
    expect(auth.hasProvider()).toBe(true);
    expect(auth.signInProblem()).toBeNull();
  });

  it("comes back to the sign-in page when the provider can't be reached", async () => {
    api(200);
    at("/deployments");
    signinRedirect.mockRejectedValue(new TypeError("Failed to fetch"));
    const auth = await load();
    expect(await auth.initAuth()).toBe("sign-in");
    expect(auth.signInProblem()).toBe("provider");
  });

  it("comes back to the sign-in page when the provider's token endpoint fails", async () => {
    api(200);
    at("/auth/callback");
    signinRedirectCallback.mockRejectedValue(new Error("Failed to fetch"));
    const auth = await load();
    expect(await auth.initAuth()).toBe("sign-in");
    expect(auth.signInProblem()).toBe("provider");
    expect(replaceState).toHaveBeenCalledWith({}, "", "/sign-in");
    expect(told()).toEqual([
      {
        url: "/api/v1/auth/sign-ins",
        authorization: undefined,
        body: { client: "web", method: "oidc", failure: { stage: "token", description: "Failed to fetch" } },
      },
    ]);
  });

  it("tells rvav of the sign-in with the new token", async () => {
    api(200);
    at("/auth/callback", "?code=abc&state=xyz");
    signinRedirectCallback.mockResolvedValue({ access_token: "tok-1", state: "/deployments" });
    const auth = await load();
    expect(await auth.initAuth()).toBe("ready");
    expect(told()).toEqual([
      { url: "/api/v1/auth/sign-ins", authorization: "Bearer tok-1", body: { client: "web", method: "oidc" } },
    ]);
    expect(JSON.stringify(fetched)).not.toContain("abc");
  });

  it("tells rvav of the sign-out before going to the provider", async () => {
    api(200);
    at("/deployments");
    signedIn = { access_token: "tok-2", expired: false };
    const auth = await load();
    expect(await auth.initAuth()).toBe("ready");
    await auth.signOut();
    expect(told()).toEqual([{ url: "/api/v1/auth/sign-out", authorization: "Bearer tok-2", body: undefined }]);
    expect(signoutRedirect).toHaveBeenCalled();
  });

  it("says the API isn't answering rather than that no provider is set", async () => {
    for (const status of [502, "down"] as const) {
      api(status);
      at("/deployments");
      const auth = await load();
      expect(await auth.initAuth()).toBe("sign-in");
      expect(auth.signInProblem()).toBe("api");
    }
  });
});

describe("a failed sign-in, as the audit log records it", () => {
  it("is the provider's refusal when it sent one back", async () => {
    const { signInFailure } = await load();
    const back = new URLSearchParams("error=access_denied&error_description=User+is+disabled&state=xyz");
    expect(signInFailure(back, new Error("User is disabled"))).toEqual({
      stage: "provider",
      error: "access_denied",
      description: "User is disabled",
    });
  });

  it("is a state that matches no sign-in under way", async () => {
    const { signInFailure } = await load();
    expect(
      signInFailure(new URLSearchParams("code=abc&state=xyz"), new Error("No matching state found in storage")),
    ).toEqual({
      stage: "state",
      description: "No matching state found in storage",
    });
  });

  it("is a refused code, with the token endpoint's error, kept short and on one line", async () => {
    const { signInFailure } = await load();
    const e = Object.assign(new Error("Code not valid\nat all " + "x".repeat(600)), { error: "invalid_grant" });
    const f = signInFailure(new URLSearchParams("code=abc&state=xyz"), e);
    expect(f.stage).toBe("token");
    expect(f.error).toBe("invalid_grant");
    expect(f.description).toHaveLength(500);
    expect(f.description).toMatch(/^Code not valid at all x+$/);
  });
});

it("opens on the sign-in page when nobody signed in before, with nothing wrong", async () => {
  api(200);
  at("/");
  const auth = await load();
  expect(await auth.initAuth()).toBe("sign-in");
  expect(auth.signInProblem()).toBeNull();
  expect(signinRedirect).not.toHaveBeenCalled();
});
