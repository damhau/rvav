// Signing in outside mock mode (ADR-0030): the sign-in page stays reachable
// when the identity provider is down, and says why it is shown.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./env", () => ({
  env: { authMode: "oidc", oidcAuthority: "", oidcClientId: "", oidcScope: "" },
  isMock: false,
}));

const signinRedirect = vi.fn();
const signinRedirectCallback = vi.fn();
let metadataDown = false;
vi.mock("oidc-client-ts", () => ({
  InMemoryWebStorage: class {},
  WebStorageStateStore: class {},
  UserManager: class {
    // The tokens are in memory only: none after a page load.
    getUser = () => (metadataDown ? Promise.reject(new TypeError("Failed to fetch")) : Promise.resolve(null));
    signinRedirect = signinRedirect;
    signinRedirectCallback = signinRedirectCallback;
  },
}));

const options = { provider: { authority: "https://sso.example/realms/rvav", client_id: "rvav", scope: "openid" } };

function api(optionsStatus: number | "down") {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      if (url.endsWith("/auth/options")) {
        if (optionsStatus === "down") return Promise.reject(new TypeError("Failed to fetch"));
        return Promise.resolve(
          new Response(optionsStatus === 200 ? JSON.stringify(options) : "bad gateway", { status: optionsStatus }),
        );
      }
      return Promise.resolve(new Response("{}", { status: 401 })); // /me: no local session
    }),
  );
}

const replaceState = vi.fn();

function at(pathname: string) {
  vi.stubGlobal("location", { ...globalThis.location, pathname, search: "", origin: "https://rvav.example" });
  vi.stubGlobal("history", { replaceState });
}

async function load() {
  vi.resetModules();
  return import("./auth");
}

beforeEach(() => {
  signinRedirect.mockReset().mockResolvedValue(undefined);
  signinRedirectCallback.mockReset();
  metadataDown = false;
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

it("opens on the sign-in page when nobody signed in before, with nothing wrong", async () => {
  api(200);
  at("/");
  const auth = await load();
  expect(await auth.initAuth()).toBe("sign-in");
  expect(auth.signInProblem()).toBeNull();
  expect(signinRedirect).not.toHaveBeenCalled();
});
