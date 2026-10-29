import { useState } from "react";
import { action, list, projectDisplayName, resourceName } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { Button, Checkbox, Switch } from "@/components/geass-ui";
import { SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

/** Who can reach this project's services from inside the cluster. */
export function ProjectNetwork({ project, data, mutable, reload }) {
  const name = resourceName(project);
  const network = project.spec?.network || {};
  const savedPrivate = (network.isolation || "Project") === "Project";
  const savedAllowed = network.allowFromProjects || [];
  const savedBlockNodes = Boolean(network.blockNodeAddresses);
  const [isPrivate, setIsPrivate] = useState(savedPrivate);
  const [blockNodes, setBlockNodes] = useState(savedBlockNodes);
  const [allowed, setAllowed] = useState(savedAllowed);
  const [busy, setBusy] = useState(false);
  const others = list(data, "projects").filter((item) => resourceName(item) !== name);

  const changed = isPrivate !== savedPrivate
    || blockNodes !== savedBlockNodes
    || [...allowed].sort().join(",") !== [...savedAllowed].sort().join(",");

  const toggle = (project, on) => setAllowed((current) => (on ? [...current, project] : current.filter((entry) => entry !== project)));

  const save = (event) => {
    event.preventDefault();
    setBusy(true);
    action(`/projects/${name}/network`, { isolation: isPrivate ? "Project" : "None", allowFromProjects: isPrivate ? allowed : [], blockNodeAddresses: blockNodes })
      .then(() => { reload(); alertUser("Saved"); })
      .catch((error) => alertError(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <form className="rw-rail-form" onSubmit={save}>
      <SettingsPanel className="rw-resource-settings-toggle-row">
        <Switch
          label="Keep this project's services private"
          description="Other projects cannot reach them."
          checked={isPrivate}
          disabled={!mutable}
          onChange={(event) => setIsPrivate(event.currentTarget.checked)}
        />
      </SettingsPanel>

      {isPrivate ? (
        <div className="rw-network-allow">
          <h3 className="rw-settings-subtitle">Projects allowed to reach this one</h3>
          {others.length ? (
            <div className="rw-rail-stack">
              {others.map((item) => {
                const other = resourceName(item);
                return (
                  <Checkbox
                    key={other}
                    label={projectDisplayName(item)}
                    checked={allowed.includes(other)}
                    disabled={!mutable}
                    onChange={(event) => toggle(other, event.currentTarget.checked)}
                  />
                );
              })}
            </div>
          ) : (
            <p className="rw-rail-hint">No other projects.</p>
          )}
        </div>
      ) : (
        <p className="rw-rail-hint">Every project can reach this project&apos;s services.</p>
      )}

      <SettingsPanel className="rw-resource-settings-toggle-row">
        <Switch
          label="Keep this project's services away from the cluster's servers"
          description="Services cannot connect to the servers' own addresses, including their own public address."
          checked={blockNodes}
          disabled={!mutable}
          onChange={(event) => setBlockNodes(event.currentTarget.checked)}
        />
      </SettingsPanel>

      <SettingsFormActions>
        <Button type="submit" disabled={!mutable || busy || !changed}>Save</Button>
      </SettingsFormActions>
    </form>
  );
}
