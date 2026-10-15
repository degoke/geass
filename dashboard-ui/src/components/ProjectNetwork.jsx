import { useState } from "react";
import { action, list, resourceDisplayName, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
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
      .then(() => { reload(); alertUser("Network settings saved"); })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <form className="rw-rail-form" onSubmit={save}>
      <p className="rw-rail-body">
        Services can always reach each other, other projects and the internet. This controls who can reach this
        project&apos;s services. It applies instantly <span className="rw-live-badge">Applies instantly</span>
      </p>
      <SettingsPanel className="rw-resource-settings-toggle-row">
        <Switch
          label="Keep this project's services private"
          description="Only this project's own services (in every environment), Geass itself, and visitors who arrive through your domains can reach them. Other projects cannot."
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
                    label={resourceDisplayName(item) === other ? other : `${resourceDisplayName(item)} (${other})`}
                    checked={allowed.includes(other)}
                    disabled={!mutable}
                    onChange={(event) => toggle(other, event.currentTarget.checked)}
                  />
                );
              })}
            </div>
          ) : (
            <p className="rw-rail-hint">There are no other projects.</p>
          )}
        </div>
      ) : (
        <p className="rw-rail-hint">Every project in the cluster can reach this project&apos;s services.</p>
      )}

      <SettingsPanel className="rw-resource-settings-toggle-row">
        <Switch
          label="Keep this project's services away from the cluster's machines"
          description="Services cannot open connections to the addresses of the servers Geass runs on, where each server's own tools and metrics answer. The Kubernetes API stays reachable for databases that need it. Geass follows the servers as they join and leave. A service that calls another through a server's address, such as its own public address, stops working."
          checked={blockNodes}
          disabled={!mutable}
          onChange={(event) => setBlockNodes(event.currentTarget.checked)}
        />
      </SettingsPanel>

      <p className="rw-rail-hint">
        Whatever you choose here, services are never allowed to read the cloud provider&apos;s credentials for the
        machine they run on.
      </p>
      <SettingsFormActions>
        <Button type="submit" disabled={!mutable || busy || !changed}>Save</Button>
      </SettingsFormActions>
    </form>
  );
}
