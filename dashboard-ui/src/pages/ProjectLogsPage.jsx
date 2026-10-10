import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { api, resourceDisplayName, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { projectResources } from "@/lib/project-resources";
import { ProjectChrome } from "@/layout/ProjectChrome";
import { Button, Muted, Select, Switch } from "@/components/geass-ui";

/** Recent container output for any service in the project (viewer roles cannot read logs). */
export function ProjectLogsPage({ project, data, environment }) {
  const apps = projectResources(data, resourceName(project), environment).apps;
  const [selected, setSelected] = useState("");
  const [follow, setFollow] = useState(true);
  const screenRef = useRef(null);
  const service = apps.find((app) => resourceName(app) === selected) || apps[0];
  const name = service ? resourceName(service) : "";
  const mutable = canMutate(data);
  const logs = useQuery({
    queryKey: ["logs", name],
    queryFn: () => api(`/api/apps/${name}/logs`),
    enabled: Boolean(name) && mutable,
    refetchInterval: follow ? 5000 : false,
  });

  useEffect(() => {
    if (follow) screenRef.current?.scrollTo({ top: screenRef.current.scrollHeight });
  }, [logs.data, follow]);

  return (
    <ProjectChrome>
      <div className="rw-pad rw-logs">
        <div className="rw-logs-bar">
          <h1 className="rw-logs-title">Logs</h1>
          {apps.length ? (
            <Select value={name} onChange={(event) => setSelected(event.currentTarget.value)} aria-label="Service">
              {apps.map((app) => <option key={resourceName(app)} value={resourceName(app)}>{resourceDisplayName(app)}</option>)}
            </Select>
          ) : null}
          <Switch label="Follow" checked={follow} onChange={(event) => setFollow(event.currentTarget.checked)} />
          <Button variant="outline" size="sm" leftSection={<RefreshCw size={14} />} disabled={!name || !mutable} loading={logs.isFetching && !logs.isPending} onClick={() => logs.refetch()}>
            Refresh
          </Button>
        </div>
        {!apps.length ? (
          <Muted size="sm" className="rw-logs-empty">Deploy a service to see its logs here.</Muted>
        ) : !mutable ? (
          <Muted size="sm" className="rw-logs-empty">Your viewer role cannot read logs.</Muted>
        ) : (
          <pre className="rw-logs-screen" ref={screenRef} aria-label={`Logs for ${name}`}>
            {logs.isPending ? "Loading logs…" : logs.isError ? logs.error.message : (logs.data?.lines || "No logs yet.")}
          </pre>
        )}
      </div>
    </ProjectChrome>
  );
}
