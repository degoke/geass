import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";

const BOOTSTRAP_QUERY_KEY = ["dashboard", "bootstrap"];

export function useDashboardLiveSync(enabled) {
  const queryClient = useQueryClient();
  const debounceRef = useRef(null);

  useEffect(() => {
    if (!enabled) return undefined;

    const refresh = () => {
      window.clearTimeout(debounceRef.current);
      debounceRef.current = window.setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: BOOTSTRAP_QUERY_KEY });
      }, 200);
    };

    const source = new EventSource("/api/events", { withCredentials: true });
    source.addEventListener("change", refresh);
    source.addEventListener("connected", refresh);
    source.addEventListener("ping", () => {});

    return () => {
      window.clearTimeout(debounceRef.current);
      source.close();
    };
  }, [enabled, queryClient]);
}
