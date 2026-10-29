import { useState } from "react";
import { action, resourceName } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { Button, Field, Input } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

const FIELDS = [
  { key: "cpu", label: "CPU", description: "Such as 4 or 500m.", number: false },
  { key: "memory", label: "Memory", description: "Such as 8Gi.", number: false },
  { key: "storage", label: "Storage", description: "Such as 50Gi.", number: false },
  { key: "pods", label: "Pods", number: true },
];

// What the form sends for each group: the whole project's names start with "project".
const GROUPS = [
  { id: "project", spec: "projectQuota", prefix: (key) => `project${key[0].toUpperCase()}${key.slice(1)}`, title: "Whole project",
    intro: "Includes pull request environments." },
  { id: "environment", spec: "environmentQuota", prefix: (key) => key, title: "Each environment" },
];

const saved = (quota) => Object.fromEntries(FIELDS.map(({ key }) => [key, quota?.[key] === undefined || quota?.[key] === null ? "" : String(quota[key])]));

/** What this project's environments may use. Off until a limit is set. */
export function ProjectQuota({ project, mutable, reload }) {
  const name = resourceName(project);
  const current = Object.fromEntries(GROUPS.map((group) => [group.id, saved(project.spec?.[group.spec])]));
  const [values, setValues] = useState(current);
  const [busy, setBusy] = useState(false);
  const usage = project.status?.quotaUsage;
  const projectQuota = project.spec?.projectQuota;

  const changed = GROUPS.some((group) => FIELDS.some(({ key }) => values[group.id][key].trim() !== current[group.id][key]));
  const anySet = GROUPS.some((group) => FIELDS.some(({ key }) => values[group.id][key].trim() !== ""));

  const set = (group, key, value) => setValues({ ...values, [group]: { ...values[group], [key]: value } });

  const save = (event) => {
    event.preventDefault();
    setBusy(true);
    const body = {};
    GROUPS.forEach((group) => FIELDS.forEach(({ key }) => { body[group.prefix(key)] = values[group.id][key].trim(); }));
    action(`/projects/${name}/quota`, body)
      .then(() => { reload(); alertUser(anySet ? "Limits saved" : "Limits removed"); })
      .catch((error) => alertError(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <form className="rw-rail-form" onSubmit={save}>
      <p className="rw-rail-body">
        Counts what services ask for, not what they use. <span className="rw-live-badge">Applies instantly</span>
      </p>
      {GROUPS.map((group) => (
        <div key={group.id} className="rw-network-allow">
          <h3 className="rw-settings-subtitle">{group.title}</h3>
          {group.intro ? <p className="rw-rail-hint">{group.intro}</p> : null}
          {group.id === "project" && usage && projectQuota ? (
            <p className="rw-rail-hint">
              Using now:{" "}
              {FIELDS.filter(({ key }) => projectQuota[key] !== undefined && projectQuota[key] !== null)
                .map(({ key, label }) => `${label} ${usage[key] ?? 0} of ${projectQuota[key]}`)
                .join(" · ")}
            </p>
          ) : null}
          {FIELDS.map(({ key, label, description, number }) => (
            <Field key={key} label={label} description={description}>
              <Input
                type={number ? "number" : "text"}
                min={number ? "1" : undefined}
                value={values[group.id][key]}
                placeholder="No limit"
                disabled={!mutable}
                onChange={(event) => set(group.id, key, event.currentTarget.value)}
                aria-label={`${group.title} ${label} limit`}
              />
            </Field>
          ))}
        </div>
      ))}
      <p className="rw-rail-hint">
        The lower limit wins. A service that would go over a limit does not start.
      </p>
      <SettingsFormActions>
        <Button type="submit" disabled={!mutable || busy || !changed}>Save</Button>
      </SettingsFormActions>
    </form>
  );
}
