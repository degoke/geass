import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw, Search } from "lucide-react";
import { api, resourceDisplayName, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { openEventSource } from "@/lib/transport";
import { projectResources } from "@/lib/project-resources";
import { logRetentionDays, loggingEnabled } from "@/lib/placement";
import { ProjectChrome } from "@/layout/ProjectChrome";
import { Button, Input, Muted, Select, Switch } from "@/components/geass-ui";

const RANGES = [
  { value: "", label: "Latest" },
  { value: "15m", label: "Last 15 minutes" },
  { value: "1h", label: "Last hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
];

// History reaches back further, so it offers longer ranges. The store keeps what the cluster's retention says.
const HISTORY_RANGES = [
  { value: "1h", label: "Last hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "72h", label: "Last 3 days" },
  { value: "168h", label: "Last 7 days" },
];

const LEVELS = [
  { value: "", label: "All levels" },
  { value: "warn", label: "Warnings and errors" },
  { value: "error", label: "Errors only" },
];

// A followed page keeps this many lines, so a busy project cannot grow the browser's memory without bound.
const MAX_LIVE_LINES = 3000;

function timeOf(line) {
  if (!line.t || line.t.startsWith("0001")) return "";
  const date = new Date(line.t);
  const pad = (value, size = 2) => String(value).padStart(size, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

function lineKey(line) {
  return `${line.t}|${line.pod}|${line.container}|${line.line}`;
}

/**
 * Container output for every service and database in the project, merged into one timeline. It shows
 * what the nodes still hold: output from a pod that has been replaced is gone, except the run before a
 * restart, which can be included. Viewer roles cannot read logs.
 */
export function ProjectLogsPage({ project, data, environment }) {
  const name = resourceName(project);
  const resources = projectResources(data, name, environment);
  const services = useMemo(
    () => [...resources.apps, ...resources.databases].map((item) => ({ value: resourceName(item), label: resourceDisplayName(item) })),
    [resources.apps, resources.databases],
  );
  const environments = project?.spec?.environments || [];

  const [service, setService] = useState("");
  const [env, setEnv] = useState(environment || "");
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState("");
  const [range, setRange] = useState("");
  const [previous, setPrevious] = useState(false);
  const [follow, setFollow] = useState(false);
  const [source, setSource] = useState("live");
  const [historyRange, setHistoryRange] = useState("1h");
  const [live, setLive] = useState([]);
  const [streamError, setStreamError] = useState("");
  const seen = useRef(new Set());
  const screenRef = useRef(null);
  const mutable = canMutate(data);

  // Typing in the search box should not read every container on every keystroke.
  useEffect(() => {
    const timer = setTimeout(() => setQuery(search.trim()), 350);
    return () => clearTimeout(timer);
  }, [search]);

  const params = useMemo(() => {
    const values = new URLSearchParams();
    if (env) values.set("environment", env);
    if (service) values.set("service", service);
    if (query) values.set("q", query);
    if (level) values.set("level", level);
    if (range) values.set("since", range);
    return values;
  }, [env, service, query, level, range]);

  const snapshotParams = useMemo(() => {
    const values = new URLSearchParams(params);
    if (previous) values.set("previous", "1");
    return values.toString();
  }, [params, previous]);

  const history = source === "history";
  const historyParams = useMemo(() => {
    const values = new URLSearchParams(params);
    values.set("since", historyRange);
    return values.toString();
  }, [params, historyRange]);

  const snapshot = useQuery({
    queryKey: ["project-logs", name, history ? "history" : "live", history ? historyParams : snapshotParams],
    queryFn: () => api(history
      ? `/api/projects/${name}/logs/history?${historyParams}`
      : `/api/projects/${name}/logs?${snapshotParams}`),
    enabled: mutable && (history || !follow),
  });

  // Following replaces the snapshot with a stream: it sends the recent lines first, then new ones.
  useEffect(() => {
    if (!follow || !mutable || history) return undefined;
    setLive([]);
    setStreamError("");
    seen.current = new Set();
    const source = openEventSource(`/api/projects/${name}/logs/stream?${params.toString()}`);
    source.onmessage = (event) => {
      try {
        const line = JSON.parse(event.data);
        const key = lineKey(line);
        // The stream starts over when the connection drops, so lines it already sent are skipped.
        if (seen.current.has(key)) return;
        seen.current.add(key);
        setLive((current) => [...current, line].slice(-MAX_LIVE_LINES));
      } catch {
        // A malformed event is skipped; the next one is independent.
      }
    };
    source.onerror = () => setStreamError("Reconnecting to the log stream…");
    source.onopen = () => setStreamError("");
    return () => source.close();
  }, [follow, mutable, name, params, history]);

  const streaming = follow && !history;
  const lines = streaming ? live : snapshot.data?.lines || [];

  useEffect(() => {
    if (streaming) screenRef.current?.scrollTo({ top: screenRef.current.scrollHeight });
  }, [live, streaming]);

  const busy = !streaming && snapshot.isPending;
  const failure = !streaming && snapshot.isError ? snapshot.error.message : "";
  const historyUnavailable = history && snapshot.data && snapshot.data.available === false ? snapshot.data.message : "";
  const logging = loggingEnabled(data);

  return (
    <ProjectChrome>
      <div className="rw-pad rw-logs">
        <div className="rw-logs-bar">
          <h1 className="rw-logs-title">Logs</h1>
          <Select value={service} onChange={(event) => setService(event.currentTarget.value)} aria-label="Service">
            <option value="">All services</option>
            {services.map((entry) => <option key={entry.value} value={entry.value}>{entry.label}</option>)}
          </Select>
          {environments.length > 1 ? (
            <Select value={env} onChange={(event) => setEnv(event.currentTarget.value)} aria-label="Environment">
              <option value="">All environments</option>
              {environments.map((entry) => <option key={entry} value={entry}>{entry}</option>)}
            </Select>
          ) : null}
          <Input
            value={search}
            onChange={(event) => setSearch(event.currentTarget.value)}
            placeholder="Search logs"
            leftSection={<Search size={14} />}
            aria-label="Search logs"
          />
          <Select value={level} onChange={(event) => setLevel(event.currentTarget.value)} aria-label="Level">
            {LEVELS.map((entry) => <option key={entry.value} value={entry.value}>{entry.label}</option>)}
          </Select>
          {logging ? (
            <Select value={source} onChange={(event) => setSource(event.currentTarget.value)} aria-label="Logs from">
              <option value="live">What the nodes hold now</option>
              <option value="history">History (kept for {logRetentionDays(data)} days)</option>
            </Select>
          ) : null}
          {history ? (
            <Select value={historyRange} onChange={(event) => setHistoryRange(event.currentTarget.value)} aria-label="Time range">
              {HISTORY_RANGES.map((entry) => <option key={entry.value} value={entry.value}>{entry.label}</option>)}
            </Select>
          ) : (
            <Select value={range} onChange={(event) => setRange(event.currentTarget.value)} aria-label="Time range">
              {RANGES.map((entry) => <option key={entry.value} value={entry.value}>{entry.label}</option>)}
            </Select>
          )}
          {!history ? <Switch label="Follow" checked={follow} onChange={(event) => setFollow(event.currentTarget.checked)} /> : null}
          {!history ? (
            <Switch
              label="Include logs from before restarts"
              checked={previous}
              disabled={follow}
              onChange={(event) => setPrevious(event.currentTarget.checked)}
            />
          ) : null}
          <Button
            variant="outline"
            size="sm"
            leftSection={<RefreshCw size={14} />}
            disabled={!mutable || streaming}
            loading={snapshot.isFetching && !snapshot.isPending}
            onClick={() => snapshot.refetch()}
          >
            Refresh
          </Button>
        </div>
        {!services.length ? (
          <Muted size="sm" className="rw-logs-empty">Deploy a service to see its logs here.</Muted>
        ) : !mutable ? (
          <Muted size="sm" className="rw-logs-empty">Your viewer role cannot read logs.</Muted>
        ) : (
          <>
            <div className="rw-logs-screen rw-logs-lines" ref={screenRef} role="log" aria-label={`Logs for ${name}`}>
              {busy ? "Loading logs…" : failure ? failure : historyUnavailable ? historyUnavailable : lines.length ? lines.map((line, index) => (
                <div key={`${lineKey(line)}|${index}`} className={`rw-log-line${line.level ? ` is-${line.level}` : ""}${line.previous ? " is-previous" : ""}`}>
                  <span className="rw-log-time">{timeOf(line)}</span>
                  <span className="rw-log-source" title={`${line.pod} · ${line.container}`}>{line.service}</span>
                  <span className="rw-log-text">{line.line}</span>
                </div>
              )) : "No logs match."}
            </div>
            <Muted size="xs">
              {streamError || (streaming
                ? "Following live. Showing the most recent lines, then new ones as they arrive."
                : history
                  ? (snapshot.data?.note || "Logs the cluster kept, including from pods that are gone.")
                  : snapshot.data?.truncated
                    ? "Showing the newest lines. Narrow the search or time range to see more."
                    : "These are the logs the nodes still hold. Output from pods that have been replaced is not kept, except the run before a restart.")}
            </Muted>
          </>
        )}
      </div>
    </ProjectChrome>
  );
}
