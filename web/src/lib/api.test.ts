import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { api, ApiError, errorMessage, onUnauthorized, passwordBytes } from "@/lib/api";

function respond(status: number, body: unknown, headers: Record<string, string> = {}) {
  return vi.fn(
    async () => new Response(body === null ? null : JSON.stringify(body), { status, headers }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  onUnauthorized(null);
});

describe("request", () => {
  it("sends JSON with the idempotency key and returns the body", async () => {
    const fetch = respond(201, { id: "h1", name: "web", state: "pending" });
    vi.stubGlobal("fetch", fetch);
    vi.stubGlobal("sessionStorage", { getItem: () => "csrf-token", setItem() {}, removeItem() {} });
    const host = await api.post<{ id: string }>(
      "/hosts",
      { name: "web" },
      { idempotencyKey: "key-1234567890abcdef" },
    );
    expect(host.id).toBe("h1");
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/hosts");
    expect(init.headers).toMatchObject({
      "Content-Type": "application/json",
      "X-CSRF-Token": "csrf-token",
      "Idempotency-Key": "key-1234567890abcdef",
    });
  });

  it("reports server errors with code and request ID", async () => {
    vi.stubGlobal("fetch", respond(500, { error: "internal_error" }, { "X-Request-ID": "req-1" }));
    const error = await api.get("/me").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(errorMessage(error)).toContain("req-1");
  });

  it("ends the session on 401 except for credential checks", async () => {
    const ended = vi.fn();
    onUnauthorized(ended);
    vi.stubGlobal("fetch", respond(401, { error: "unauthorized" }));
    await api.get("/hosts").catch(() => undefined);
    expect(ended).toHaveBeenCalledTimes(1);
    await api
      .post("/auth/reauthenticate", { password: "x" }, { credentialCheck: true })
      .catch(() => undefined);
    expect(ended).toHaveBeenCalledTimes(1);
  });

  it("maps network failures to a readable message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Promise.reject(new TypeError("offline"))),
    );
    const error = await api.get("/me").catch((e: unknown) => e);
    expect((error as ApiError).code).toBe("network_error");
    expect(errorMessage(error)).toContain("接続");
  });
});

describe("passwordBytes", () => {
  it("counts UTF-8 bytes like the server", () => {
    expect(passwordBytes("abcdef")).toBe(6);
    expect(passwordBytes("あいう")).toBe(9);
  });
});
