import { afterEach, expect, it, vi } from "vitest";
import { connectHints, validHint } from "./realtime";
class FakeSocket {
  onopen: WebSocket["onopen"] = null;
  onclose: WebSocket["onclose"] = null;
  onerror: WebSocket["onerror"] = null;
  onmessage: WebSocket["onmessage"] = null;
  close = vi.fn();
  open() {
    this.onopen?.call(this as unknown as WebSocket, new Event("open"));
  }
  disconnect() {
    this.onclose?.call(this as unknown as WebSocket, new CloseEvent("close"));
  }
  message(data: unknown) {
    this.onmessage?.call(
      this as unknown as WebSocket,
      new MessageEvent("message", { data }),
    );
  }
}
const hint = JSON.stringify({
  version: 1,
  event: "job.changed",
  resource_id: "id",
  occurred_at: "2026-10-05T07:53:43Z",
  hint: { status: "RUNNING" },
});
afterEach(() => vi.useRealTimers());
it("reconnect snapshots, periodic repair, duplicate bursts and disposal are bounded", () => {
  vi.useFakeTimers();
  const sockets: FakeSocket[] = [],
    refresh = vi.fn(),
    state = vi.fn();
  const close = connectHints(refresh, state, () => {
    const s = new FakeSocket();
    sockets.push(s);
    return s;
  });
  sockets[0].open();
  expect(refresh).toHaveBeenCalledTimes(1);
  for (let i = 0; i < 200; i++) sockets[0].message(hint);
  expect(vi.getTimerCount()).toBe(2);
  vi.advanceTimersByTime(250);
  expect(refresh).toHaveBeenCalledTimes(2);
  sockets[0].message("malformed");
  sockets[0].disconnect();
  sockets[0].disconnect();
  vi.advanceTimersByTime(1000);
  expect(sockets).toHaveLength(2);
  sockets[1].open();
  expect(refresh).toHaveBeenCalledTimes(3);
  vi.advanceTimersByTime(30000);
  expect(refresh).toHaveBeenCalledTimes(4);
  sockets[1].message(
    JSON.stringify({
      version: 1,
      event: "system.changed",
      resource_id: "system",
      occurred_at: "2026-10-05T07:53:43Z",
      hint: { status: "DEGRADED" },
    }),
  );
  expect(state).toHaveBeenLastCalledWith("degraded");
  close();
  expect(vi.getTimerCount()).toBe(0);
  expect(sockets[1].close).toHaveBeenCalledOnce();
  vi.advanceTimersByTime(60000);
  expect(refresh).toHaveBeenCalledTimes(4);
});
it("caps retry delay and handles construction errors", () => {
  vi.useFakeTimers();
  const factory = vi.fn(() => {
    throw Error("offline");
  });
  const refresh = vi.fn(),
    stop = connectHints(refresh, vi.fn(), factory);
  vi.advanceTimersByTime(100000);
  expect(factory.mock.calls.length).toBeLessThan(15);
  stop();
  expect(vi.getTimerCount()).toBe(0);
});
it("rejects future versions, oversized and malformed envelopes", () => {
  expect(validHint(hint)).toBe(true);
  for (const v of [
    "{}",
    "null",
    "x".repeat(1025),
    hint.replace('"version":1', '"version":2'),
    hint.replace("job.changed", "unknown"),
  ])
    expect(validHint(v)).toBe(false);
});
