import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

function bootstrapShouldPoll(error) {
  const status = error?.status;
  return status !== 401 && status !== 404;
}

export function useBootstrap() {
  return useQuery({
    queryKey: ["dashboard", "bootstrap"],
    queryFn: () => api("/api/bootstrap"),
    staleTime: 4000,
    retry: (count, error) => bootstrapShouldPoll(error) && count < 2,
    refetchInterval: (query) => (bootstrapShouldPoll(query.state.error) ? 8000 : false),
  });
}

export function dashboardSession(data) {
  return data?.platform?.session || { username: "", role: "viewer", canMutate: false };
}

export function canMutate(data) {
  return Boolean(dashboardSession(data).canMutate);
}
