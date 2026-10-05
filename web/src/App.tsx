import { useEffect, useState } from "react";
import {
  APIError,
  command,
  type Job,
  type Worker,
  type Schedule,
  type Summary,
  type Attempt,
} from "./api";
import { useSnapshot } from "./hooks";
import { connectHints, type ConnectionState } from "./realtime";
import "./style.css";

export function Badge({ status }: { status: string }) {
  return (
    <span className={`badge ${status.toLowerCase()}`}>
      {status.replaceAll("_", " ")}
    </span>
  );
}
function Time({ value }: { value: string | null }) {
  return value ? (
    <time title={value} dateTime={value}>
      {new Date(value).toLocaleString()}
    </time>
  ) : (
    <span className="muted">—</span>
  );
}
function JSONView({ label, value }: { label: string; value: unknown }) {
  const text = JSON.stringify(value, null, 2) ?? "null";
  return (
    <details>
      <summary>
        {label} <small>{text.length.toLocaleString()} characters</small>
      </summary>
      <pre>
        {text.slice(0, 16384)}
        {text.length > 16384
          ? "\n… Render limited to 16 KiB; use the REST response for full content."
          : ""}
      </pre>
    </details>
  );
}
function Feedback({
  loading,
  error,
  empty,
}: {
  loading: boolean;
  error: string;
  empty?: boolean;
}) {
  return (
    <div aria-live="polite">
      {error && (
        <p role="alert" className="error">
          {error}. Any displayed snapshot may be stale.
        </p>
      )}
      {loading && <p className="muted">Loading / resyncing from PostgreSQL…</p>}
      {!loading && !error && empty && (
        <p className="empty">No records on this page.</p>
      )}
    </div>
  );
}
export function Action({
  label,
  path,
  onDone,
  disabled = false,
}: {
  label: string;
  path: string;
  onDone: () => void;
  disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false),
    [message, setMessage] = useState("");
  return (
    <span className="action">
      <button
        disabled={busy || disabled}
        onClick={async () => {
          if (
            !window.confirm(
              `${label}? The server will validate this operation.`,
            )
          )
            return;
          setBusy(true);
          setMessage("");
          try {
            await command(path);
            setMessage("Command accepted. Refreshing current state…");
            onDone();
          } catch (e) {
            setMessage(
              e instanceof APIError
                ? `${e.status} ${e.code}: ${e.message}`
                : e instanceof Error
                  ? e.message
                  : "Request failed",
            );
            onDone();
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Submitting…" : label}
      </button>
      {message && <span role="status">{message}</span>}
    </span>
  );
}
function Overview({ revision }: { revision: number }) {
  const s = useSnapshot<Summary>("dashboard/summary", revision);
  return (
    <>
      <h1>Overview</h1>
      <p className="muted">Current persisted state, read from PostgreSQL.</p>
      <Feedback {...s} />
      {s.data && (
        <>
          <div className="stats">
            <article>
              <span>Eligible queue depth</span>
              <strong>{s.data.queue_depth}</strong>
              <small>
                Due QUEUED jobs with attempt budget; capability matching is not
                included.
              </small>
            </article>
            <article>
              <span>Worker active jobs</span>
              <strong>{s.data.active_jobs}</strong>
              <small>Heartbeat counts from non-OFFLINE workers.</small>
            </article>
          </div>
          <h2>Jobs</h2>
          <div className="counts">
            {Object.entries(s.data.jobs).map(([status, n]) => (
              <article key={status}>
                <Badge status={status} />
                <strong>{n}</strong>
              </article>
            ))}
          </div>
          <h2>Workers</h2>
          <div className="counts">
            {Object.entries(s.data.workers).map(([status, n]) => (
              <article key={status}>
                <Badge status={status} />
                <strong>{n}</strong>
              </article>
            ))}
          </div>
          <h2>Schedules</h2>
          <div className="counts">
            {Object.entries(s.data.schedules).map(([status, n]) => (
              <article key={status}>
                <Badge status={status} />
                <strong>{n}</strong>
              </article>
            ))}
          </div>
        </>
      )}
      <p className="note">
        Throughput, failure rate and latency metrics are planned for Phase 9.
        Persistent application logs are not available.
      </p>
    </>
  );
}
function Pager({
  next,
  history,
  advance,
  back,
}: {
  next: string | null;
  history: string[];
  advance: () => void;
  back: () => void;
}) {
  return (
    <div className="pager">
      <button disabled={!history.length} onClick={back}>
        Previous
      </button>
      <span>Page {history.length + 1}</span>
      <button disabled={!next} onClick={advance}>
        Next
      </button>
    </div>
  );
}
function usePage() {
  const [cursor, setCursor] = useState(""),
    [history, setHistory] = useState<string[]>([]);
  return {
    cursor,
    history,
    advance: (next: string) => {
      setHistory([...history, cursor]);
      setCursor(next);
    },
    back: () => {
      setCursor(history.at(-1) ?? "");
      setHistory(history.slice(0, -1));
    },
    query: `?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
  };
}
export function Jobs({
  revision,
  dead = false,
  refresh,
}: {
  revision: number;
  dead?: boolean;
  refresh: () => void;
}) {
  const p = usePage(),
    s = useSnapshot<{ jobs: Job[]; next_cursor: string | null }>(
      `${dead ? "dead-letter" : "jobs"}${p.query}`,
      revision,
    );
  return (
    <>
      <h1>{dead ? "Dead letter" : "Jobs"}</h1>
      <p className="muted">Newest submissions first · 20 per page</p>
      <Feedback {...s} empty={s.data?.jobs.length === 0} />
      {s.data && (
        <>
          <div className="table">
            <table>
              <thead>
                <tr>
                  <th>Job / type</th>
                  <th>Status</th>
                  <th>Priority</th>
                  <th>Attempts</th>
                  <th>Worker</th>
                  <th>Created</th>
                  {dead && <th>Last result / control</th>}
                </tr>
              </thead>
              <tbody>
                {s.data.jobs.map((j) => (
                  <tr key={j.id}>
                    <td>
                      <a href={`#/jobs/${j.id}`}>{j.id.slice(0, 8)}</a>
                      <small>{j.type}</small>
                    </td>
                    <td>
                      <Badge status={j.status} />
                    </td>
                    <td>{j.priority}</td>
                    <td>
                      {j.attempt_count} / {j.max_attempts}
                    </td>
                    <td title={j.assigned_worker ?? ""}>
                      {j.assigned_worker?.slice(0, 8) ?? "—"}
                    </td>
                    <td>
                      <Time value={j.created_at} />
                    </td>
                    {dead && (
                      <td>
                        <JSONView label="Last result" value={j.result} />
                        <Action
                          label="Redrive"
                          path={`jobs/${j.id}/retry`}
                          onDone={refresh}
                        />
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pager
            next={s.data.next_cursor}
            history={p.history}
            advance={() => p.advance(s.data!.next_cursor!)}
            back={p.back}
          />
        </>
      )}
    </>
  );
}
export function JobDetail({
  id,
  revision,
  refresh,
}: {
  id: string;
  revision: number;
  refresh: () => void;
}) {
  const s = useSnapshot<Job>(`jobs/${encodeURIComponent(id)}`, revision),
    a = useSnapshot<{ attempts: Attempt[] }>(
      `jobs/${encodeURIComponent(id)}/attempts`,
      revision,
    ),
    j = s.data;
  return (
    <>
      <a href="#/jobs">← Jobs</a>
      <h1>Job detail</h1>
      <Feedback {...s} />
      {j && (
        <>
          <div className="resource">
            <code>{j.id}</code>
            <Badge status={j.status} />
          </div>
          <div className="controls">
            {["QUEUED", "RETRYING", "RUNNING"].includes(j.status) && (
              <Action
                label="Cancel job"
                path={`jobs/${j.id}/cancel`}
                onDone={refresh}
              />
            )}
            {j.status === "DEAD_LETTER" && (
              <Action
                label="Redrive"
                path={`jobs/${j.id}/retry`}
                onDone={refresh}
              />
            )}
          </div>
          <dl>
            {(
              [
                "type",
                "priority",
                "attempt_count",
                "max_attempts",
                "timeout",
                "idempotency_key",
                "assigned_worker",
                "schedule_id",
              ] as const
            ).map((k) => (
              <div key={k}>
                <dt>{k.replaceAll("_", " ")}</dt>
                <dd>{j[k] ?? "—"}</dd>
              </div>
            ))}
            <div>
              <dt>Required capabilities</dt>
              <dd>{j.required_capabilities.join(", ") || "Unrestricted"}</dd>
            </div>
            {(
              [
                "scheduled_at",
                "scheduled_for",
                "lease_expiry",
                "retry_at",
                "cancel_requested_at",
                "created_at",
                "started_at",
                "finished_at",
              ] as const
            ).map((k) => (
              <div key={k}>
                <dt>{k.replaceAll("_", " ")}</dt>
                <dd>
                  <Time value={j[k]} />
                </dd>
              </div>
            ))}
          </dl>
          <JSONView label="Payload" value={j.payload} />
          <JSONView label="Result" value={j.result} />
        </>
      )}
      <h2>Attempts</h2>
      <Feedback {...a} empty={a.data?.attempts.length === 0} />
      {a.data?.attempts.map((v) => (
        <article className="attempt" key={v.id}>
          <div>
            <strong>Attempt {v.attempt_number}</strong>{" "}
            <Badge status={v.status} />
          </div>
          <p>
            Worker <code>{v.worker_id}</code>
          </p>
          <p>
            <Time value={v.started_at} /> → <Time value={v.finished_at} />
          </p>
          {v.error && <p className="error">{v.error}</p>}
          <JSONView label="Attempt result" value={v.result} />
        </article>
      ))}
      <p className="note">
        Structured attempt outcomes only. Persistent application logs are not
        implemented.
      </p>
    </>
  );
}
function Workers({ revision }: { revision: number }) {
  const p = usePage(),
    s = useSnapshot<{ workers: Worker[]; next_cursor: string | null }>(
      `workers${p.query}`,
      revision,
    );
  return (
    <>
      <h1>Workers</h1>
      <p className="muted">
        Immutable UUID order · heartbeats reconciled every 30 seconds
      </p>
      <Feedback {...s} empty={s.data?.workers.length === 0} />
      {s.data && (
        <>
          <div className="table">
            <table>
              <thead>
                <tr>
                  <th>Worker</th>
                  <th>Status</th>
                  <th>Capabilities</th>
                  <th>Concurrency</th>
                  <th>Active jobs</th>
                  <th>Last heartbeat</th>
                </tr>
              </thead>
              <tbody>
                {s.data.workers.map((v) => (
                  <tr key={v.worker_id}>
                    <td>
                      <code title={v.worker_id}>{v.worker_id.slice(0, 8)}</code>
                    </td>
                    <td>
                      <Badge status={v.status} />
                    </td>
                    <td>{v.capabilities.join(", ") || "None"}</td>
                    <td>{v.concurrency}</td>
                    <td>{v.active_jobs}</td>
                    <td>
                      <Time value={v.last_heartbeat} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pager
            next={s.data.next_cursor}
            history={p.history}
            advance={() => p.advance(s.data!.next_cursor!)}
            back={p.back}
          />
        </>
      )}
    </>
  );
}
function Schedules({
  revision,
  refresh,
}: {
  revision: number;
  refresh: () => void;
}) {
  const p = usePage(),
    s = useSnapshot<{ schedules: Schedule[]; next_cursor: string | null }>(
      `schedules${p.query}`,
      revision,
    );
  return (
    <>
      <h1>Schedules</h1>
      <Feedback {...s} empty={s.data?.schedules.length === 0} />
      {s.data && (
        <>
          <div className="table">
            <table>
              <thead>
                <tr>
                  <th>Schedule / type</th>
                  <th>Status</th>
                  <th>Priority / capabilities</th>
                  <th>Interval</th>
                  <th>Next run</th>
                  <th>Control</th>
                </tr>
              </thead>
              <tbody>
                {s.data.schedules.map((v) => (
                  <tr key={v.id}>
                    <td>
                      <code title={v.id}>{v.id.slice(0, 8)}</code>
                      <small>{v.type}</small>
                    </td>
                    <td>
                      <Badge status={v.status} />
                    </td>
                    <td>
                      {v.priority}
                      <small>
                        {v.required_capabilities.join(", ") || "Unrestricted"}
                      </small>
                    </td>
                    <td>{v.interval_seconds}s</td>
                    <td>
                      <Time value={v.next_run_at} />
                    </td>
                    <td>
                      <Action
                        label="Cancel schedule"
                        path={`schedules/${v.id}/cancel`}
                        disabled={v.status !== "ACTIVE"}
                        onDone={refresh}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pager
            next={s.data.next_cursor}
            history={p.history}
            advance={() => p.advance(s.data!.next_cursor!)}
            back={p.back}
          />
        </>
      )}
    </>
  );
}
export default function App() {
  const [route, setRoute] = useState(location.hash.slice(1) || "/"),
    [revision, setRevision] = useState(0),
    [connection, setConnection] = useState<ConnectionState>("connecting");
  const refresh = () => setRevision((v) => v + 1);
  useEffect(() => {
    const changed = () => setRoute(location.hash.slice(1) || "/");
    addEventListener("hashchange", changed);
    return () => removeEventListener("hashchange", changed);
  }, []);
  useEffect(
    () => connectHints(() => setRevision((v) => v + 1), setConnection),
    [],
  );
  let page;
  if (route === "/") page = <Overview revision={revision} />;
  else if (route === "/jobs")
    page = <Jobs revision={revision} refresh={refresh} />;
  else if (route.startsWith("/jobs/"))
    page = (
      <JobDetail id={route.slice(6)} revision={revision} refresh={refresh} />
    );
  else if (route === "/workers") page = <Workers revision={revision} />;
  else if (route === "/schedules")
    page = <Schedules revision={revision} refresh={refresh} />;
  else if (route === "/dead-letter")
    page = <Jobs key="dead" dead revision={revision} refresh={refresh} />;
  else page = <h1>Page not found</h1>;
  return (
    <div className="shell">
      <aside>
        <a className="brand" href="#/">
          F<span>FlowForge</span>
        </a>
        <p className="eyebrow">Operations dashboard</p>
        <nav>
          {[
            ["/", "Overview"],
            ["/jobs", "Jobs"],
            ["/workers", "Workers"],
            ["/schedules", "Schedules"],
            ["/dead-letter", "Dead letter"],
          ].map(([path, label]) => (
            <a
              key={path}
              href={`#${path}`}
              aria-current={route === path ? "page" : undefined}
            >
              {label}
            </a>
          ))}
        </nav>
        <p className="boundary">
          LOCAL / DEMO
          <br />
          No authentication
        </p>
      </aside>
      <div className="workspace">
        <header>
          <span>PostgreSQL authoritative</span>
          <div role="status" className={`connection ${connection}`}>
            ● {connection} · REST resync on reconnect / every 30s{" "}
            <button onClick={refresh}>Refresh</button>
          </div>
        </header>
        <main key={route}>{page}</main>
        <footer>
          FlowForge · Phase 8 · Transient hints, authoritative REST snapshots
        </footer>
      </div>
    </div>
  );
}
