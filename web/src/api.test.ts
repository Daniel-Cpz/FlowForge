import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, request } from "./api";
afterEach(() => vi.unstubAllGlobals());
describe("REST errors", () => {
  for (const status of [400, 409, 500])
    it(`preserves ${status} stable envelope`, async () => {
      vi.stubGlobal(
        "fetch",
        vi
          .fn()
          .mockResolvedValue({
            ok: false,
            status,
            json: async () => ({
              error: {
                code: "CONTROL_CONFLICT",
                message: "Cannot accept command",
              },
            }),
          }),
      );
      await expect(request("jobs")).rejects.toMatchObject({
        status,
        code: "CONTROL_CONFLICT",
        message: "Cannot accept command",
      });
    });
  it("rejects invalid JSON and fallback errors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 502,
        json: async () => {
          throw Error("html");
        },
      }),
    );
    await expect(request("jobs")).rejects.toBeInstanceOf(APIError);
  });
  it("sends abort signal without storing credentials", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue({ ok: true, json: async () => ({ jobs: [] }) });
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    await request("jobs", { signal });
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/jobs",
      expect.objectContaining({ signal }),
    );
  });
});
