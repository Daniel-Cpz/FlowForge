import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Jobs, JobDetail, Action } from "./App";
import type { Job } from "./api";
const job: Job = {
  id: "11111111-1111-4111-8111-111111111111",
  type: "SLEEP",
  status: "QUEUED",
  priority: 0,
  payload: { duration_ms: 1 },
  result: null,
  attempt_count: 0,
  max_attempts: 3,
  timeout: 300,
  idempotency_key: null,
  required_capabilities: [],
  scheduled_at: null,
  schedule_id: null,
  scheduled_for: null,
  assigned_worker: null,
  lease_expiry: null,
  retry_at: null,
  cancel_requested_at: null,
  created_at: "2026-10-05T07:53:43Z",
  started_at: null,
  finished_at: null,
};
const response = (body: unknown, status = 200) => ({
  ok: status < 400,
  status,
  json: async () => body,
});
afterEach(() => vi.unstubAllGlobals());
it("Jobs updates from a fresh REST snapshot after hints", async () => {
  let status = "QUEUED";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ jobs: [{ ...job, status }], next_cursor: null }),
    ),
  );
  const view = render(<Jobs revision={0} refresh={vi.fn()} />);
  await screen.findByText("QUEUED");
  status = "RUNNING";
  view.rerender(<Jobs revision={1} refresh={vi.fn()} />);
  await screen.findByText("RUNNING");
  expect(screen.queryByText("QUEUED")).not.toBeInTheDocument();
});
it("discards stale detail response when an event arrives before initial load", async () => {
  let resolve: (body: unknown) => void = () => {};
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/attempts")) return response({ attempts: [] });
      calls++;
      if (calls === 1)
        return new Promise((r) => {
          resolve = r;
        });
      return response({ ...job, status: "SUCCEEDED" });
    }),
  );
  const view = render(<JobDetail id={job.id} revision={0} refresh={vi.fn()} />);
  view.rerender(<JobDetail id={job.id} revision={1} refresh={vi.fn()} />);
  await screen.findByText("SUCCEEDED");
  await act(async () => resolve(response(job)));
  expect(screen.queryByText("QUEUED")).not.toBeInTheDocument();
});
it("cancel does not change authoritative state until server and refetch complete", async () => {
  let state = "RUNNING",
    finish: (v: unknown) => void = () => {};
  vi.stubGlobal(
    "confirm",
    vi.fn(() => true),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, opts: RequestInit) => {
      if (opts.method === "POST")
        return new Promise((r) => {
          finish = r;
        });
      if (path.endsWith("/attempts")) return response({ attempts: [] });
      return response({ ...job, status: state });
    }),
  );
  function Detail() {
    const [revision, setRevision] = useState(0);
    return (
      <JobDetail
        id={job.id}
        revision={revision}
        refresh={() => setRevision((v) => v + 1)}
      />
    );
  }
  render(<Detail />);
  await screen.findByText("RUNNING");
  fireEvent.click(screen.getByText("Cancel job"));
  expect(screen.getByText("RUNNING")).toBeInTheDocument();
  expect(screen.getByText("Submitting…")).toBeDisabled();
  expect(screen.queryByText("CANCELLED")).not.toBeInTheDocument();
  state = "CANCELLED";
  await act(async () => finish(response({ ...job, status: state })));
  await screen.findByText("CANCELLED");
  expect(screen.queryByText("RUNNING")).not.toBeInTheDocument();
});
for (const status of [409, 400, 500])
  it(`redrive surfaces ${status} and refetches rather than fabricating success`, async () => {
    vi.stubGlobal(
      "confirm",
      vi.fn(() => true),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response(
          {
            error: {
              code: "JOB_CONTROL_CONFLICT",
              message: "Cannot accept command",
            },
          },
          status,
        ),
      ),
    );
    const done = vi.fn();
    render(
      <Action label="Redrive" path={`jobs/${job.id}/retry`} onDone={done} />,
    );
    fireEvent.click(screen.getByText("Redrive"));
    await screen.findByText(
      `${status} JOB_CONTROL_CONFLICT: Cannot accept command`,
    );
    expect(done).toHaveBeenCalledOnce();
    expect(
      screen.queryByText("Command accepted. Refreshing current state…"),
    ).not.toBeInTheDocument();
  });
it("successful redrive and schedule cancel refetch only after confirmation", async () => {
  vi.stubGlobal(
    "confirm",
    vi.fn(() => false),
  );
  const fetch = vi.fn(async () => response({}));
  vi.stubGlobal("fetch", fetch);
  const done = vi.fn();
  render(
    <Action label="Cancel schedule" path="schedules/id/cancel" onDone={done} />,
  );
  fireEvent.click(screen.getByText("Cancel schedule"));
  expect(fetch).not.toHaveBeenCalled();
  vi.stubGlobal(
    "confirm",
    vi.fn(() => true),
  );
  fireEvent.click(screen.getByText("Cancel schedule"));
  await waitFor(() => expect(done).toHaveBeenCalledOnce());
  expect(fetch).toHaveBeenCalledWith(
    "/api/v1/schedules/id/cancel",
    expect.objectContaining({ method: "POST" }),
  );
});
it("escapes payload text and limits large JSON rendering", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) =>
      response(
        path.endsWith("/attempts")
          ? { attempts: [] }
          : { ...job, payload: '<img onerror="alert(1)">' + "x".repeat(20000) },
      ),
    ),
  );
  render(<JobDetail id={job.id} revision={0} refresh={vi.fn()} />);
  await screen.findByText(/Payload/);
  expect(document.querySelector("img")).toBeNull();
  expect(
    screen.getByText(/Render limited to 16 KiB/).textContent!.length,
  ).toBeLessThan(17000);
});
