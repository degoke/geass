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
  const [isPrivate, setIsPrivate] = useState(savedPrivate);
  const [allowed, setAllowed] = useState(savedAllowed);
  const [busy, setBusy] = useState(false);
  const others = list(data, "projects").filter((item) => resourceName(item) !== name);

  const changed = isPrivate !== savedPrivate
    || [...allowed].sort().join(",") !== [...savedAllowed].sort().join(",");

  const toggle = (project, on) => setAllowed((current) => (on ? [...current, project] : current.filter((entry) => entry !== project)));

  const save = (event) => {
    event.preventDefault();
    setBusy(true);
    action(`/projects/${name}/network`, { isolation: isPrivate ? "Project" : "None", allowFromProjects: isPrivate ? allowed : [] })
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
