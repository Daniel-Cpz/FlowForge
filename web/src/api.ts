export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(`/api/v1/${path}`, {
    ...options,
    headers: { Accept: "application/json", ...options.headers },
  });
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new APIError(
      response.status,
      "INVALID_RESPONSE",
      "Server returned an invalid response",
    );
  }
  if (!response.ok) {
    const e = body as { error?: { code?: unknown; message?: unknown } };
    throw new APIError(
      response.status,
      typeof e.error?.code === "string" ? e.error.code : "REQUEST_FAILED",
      typeof e.error?.message === "string" ? e.error.message : "Request failed",
    );
  }
  return body as T;
}
export const command = (path: string) => request(path, { method: "POST" });
export interface Job {
  id: string;
  type: string;
  status: string;
  priority: number;
  payload: unknown;
  result: unknown;
  attempt_count: number;
  max_attempts: number;
  timeout: number;
  idempotency_key: string | null;
  required_capabilities: string[];
  scheduled_at: string | null;
  schedule_id: string | null;
  scheduled_for: string | null;
  assigned_worker: string | null;
  lease_expiry: string | null;
  retry_at: string | null;
  cancel_requested_at: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}
export interface Attempt {
  id: string;
  job_id: string;
  worker_id: string;
  attempt_number: number;
  status: string;
  result: unknown;
  error: string | null;
  started_at: string;
  finished_at: string | null;
}
export interface Worker {
  worker_id: string;
  status: string;
  capabilities: string[];
  concurrency: number;
  active_jobs: number;
  last_heartbeat: string | null;
}
export interface Schedule {
  id: string;
  status: string;
  type: string;
  priority: number;
  required_capabilities: string[];
  interval_seconds: number;
  next_run_at: string;
  created_at: string;
  updated_at: string;
}
export interface Summary {
  jobs: Record<string, number>;
  queue_depth: number;
  workers: Record<string, number>;
  active_jobs: number;
  schedules: Record<string, number>;
}
