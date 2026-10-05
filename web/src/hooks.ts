import { useEffect, useState } from "react";
import { request } from "./api";
export function useSnapshot<T>(path: string, revision: number) {
  const [value, setValue] = useState<{ path: string; data: T } | null>(null),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true);
  useEffect(() => {
    const abort = new AbortController();
    let active = true;
    setLoading(true);
    setError("");
    request<T>(path, { signal: abort.signal })
      .then((data) => {
        if (active) setValue({ path, data });
      })
      .catch((e) => {
        if (active) setError(e instanceof Error ? e.message : "Request failed");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      abort.abort();
    };
  }, [path, revision]);
  return { data: value?.path === path ? value.data : null, error, loading };
}
