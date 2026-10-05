export type ConnectionState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "degraded"
  | "stopped";
type Socket = Pick<
  WebSocket,
  "onopen" | "onclose" | "onerror" | "onmessage" | "close"
>;
export function validHint(data: unknown): boolean {
  if (typeof data !== "string" || data.length > 1024) return false;
  try {
    const e = JSON.parse(data);
    return (
      e?.version === 1 &&
      [
        "job.changed",
        "worker.changed",
        "schedule.changed",
        "system.changed",
      ].includes(e.event) &&
      typeof e.resource_id === "string" &&
      typeof e.occurred_at === "string" &&
      Number.isFinite(Date.parse(e.occurred_at)) &&
      typeof e.hint === "object" &&
      e.hint !== null
    );
  } catch {
    return false;
  }
}
// One socket, one retry timer, one coalescing timer. Hints never mutate entities.
export function connectHints(
  onRefresh: () => void,
  onState: (state: ConnectionState) => void,
  makeSocket: () => Socket = () =>
    new WebSocket(
      `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/v1/ws`,
    ),
) {
  let stopped = false,
    socket: Socket | undefined,
    retry: ReturnType<typeof setTimeout> | undefined,
    batch: ReturnType<typeof setTimeout> | undefined,
    delay = 1000;
  const refresh = () => {
    if (batch === undefined)
      batch = setTimeout(() => {
        batch = undefined;
        if (!stopped) onRefresh();
      }, 250);
  };
  const open = () => {
    if (stopped) return;
    onState(socket ? "reconnecting" : "connecting");
    try {
      socket = makeSocket();
    } catch {
      schedule();
      return;
    }
    const current = socket;
    current.onopen = () => {
      if (stopped || socket !== current) return;
      delay = 1000;
      onState("connected");
      onRefresh();
    };
    current.onmessage = (message) => {
      if (stopped || socket !== current || !validHint(message.data)) return;
      const e = JSON.parse(message.data as string);
      if (e.event === "system.changed")
        onState(e.hint.status === "LIVE" ? "connected" : "degraded");
      refresh();
    };
    current.onerror = () => {
      current.close();
    };
    current.onclose = () => {
      if (stopped || socket !== current) return;
      onState("reconnecting");
      schedule();
    };
  };
  function schedule() {
    if (stopped || retry !== undefined) return;
    retry = setTimeout(() => {
      retry = undefined;
      open();
    }, delay);
    delay = Math.min(delay * 2, 15000);
  }
  open();
  const reconcile = setInterval(onRefresh, 30000);
  return () => {
    stopped = true;
    if (retry !== undefined) clearTimeout(retry);
    if (batch !== undefined) clearTimeout(batch);
    clearInterval(reconcile);
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      socket.close();
    }
    onState("stopped");
  };
}
