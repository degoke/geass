import { Link } from "@tanstack/react-router";

const PROBLEM_TEXT = {
  MemoryPressure: "memory high",
  DiskPressure: "disk full",
  PIDPressure: "too many processes",
  NetworkUnavailable: "network down",
};

function nodeState(node) {
  if (!node.ready) return "down";
  if ((node.pressure || []).some((entry) => entry !== "Unschedulable")) return "pressure";
  if (node.schedulable === false || (node.pressure || []).includes("Unschedulable")) return "paused";
  return "ok";
}

function problemText(node, state) {
  if (state === "down") return "not ready";
  if (state === "paused") return "unschedulable";
  const entry = (node.pressure || []).find((item) => item !== "Unschedulable");
  return PROBLEM_TEXT[entry] || "under pressure";
}

/** One bar per node, so a struggling machine is visible from every page. */
export function ClusterHealth({ data }) {
  const nodes = data?.platform?.capacity?.nodes || [];
  if (!nodes.length) return null;
  const states = nodes.map((node) => ({ node, state: nodeState(node) }));
  const worst = states.find((entry) => entry.state === "down") || states.find((entry) => entry.state === "pressure") || states.find((entry) => entry.state === "paused");
  const note = worst ? `${worst.node.name} ${problemText(worst.node, worst.state)}` : "all healthy";
  return (
    <Link to="/cluster" className="rw-health" title="Open cluster usage">
      <span className="rw-health-label">{nodes.length} {nodes.length === 1 ? "NODE" : "NODES"}</span>
      <span className="rw-health-bars" aria-hidden="true">
        {states.map(({ node, state }) => (
          <span key={node.name} className={`rw-health-bar is-${state}`} />
        ))}
      </span>
      <span className={worst ? "rw-health-note is-problem" : "rw-health-note"}>{note}</span>
    </Link>
  );
}
