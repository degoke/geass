import { useState } from "react";
import { action, resourceName } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { projectRegion, tagsInRegion } from "@/lib/placement";
import { Button, Checkbox } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

/**
 * The node tags every workload of this project must run on, such as hipaa. The project's services,
 * databases, buckets, builds and the jobs that back them up all stay on nodes that carry the tags, and
 * those nodes take no other project's work.
 */
export function ProjectCompliance({ project, data, mutable, reload }) {
  const name = resourceName(project);
  const saved = project.spec?.requiredTags || [];
  const [selected, setSelected] = useState(saved);
  const [busy, setBusy] = useState(false);

  const region = projectRegion(project, data);
  const candidates = tagsInRegion(data, region);
  // A tag the project requires stays listed even when no node carries it any more, so it can be seen
  // and removed.
  const known = new Set(candidates.map((tag) => tag.name));
  const missing = saved.filter((tag) => !known.has(tag));
  const changed = [...selected].sort().join(",") !== [...saved].sort().join(",");

  const toggle = (tag, on) => setSelected((current) => (on ? [...current.filter((entry) => entry !== tag), tag] : current.filter((entry) => entry !== tag)));

  const save = (event) => {
    event.preventDefault();
    setBusy(true);
    action(`/projects/${name}/compliance`, { requiredTags: selected.join(",") })
      .then(() => { reload(); alertUser("Saved"); })
      .catch((error) => alertError(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <form className="rw-rail-form" onSubmit={save}>
      <p className="rw-rail-body">
        Nodes with these tags take no other project&apos;s work. Existing databases must be restored into new ones.
      </p>
      {candidates.length || missing.length ? (
        <div className="rw-rail-stack">
          {candidates.map((tag) => (
            <Checkbox
              key={tag.name}
              label={`${tag.name} (${tag.nodes} ${tag.nodes === 1 ? "node" : "nodes"}${tag.compliance ? ", compliance" : tag.reserved ? ", reserved" : ""})`}
              description={tag.reserved || tag.compliance ? undefined : "Not a compliance tag yet."}
              checked={selected.includes(tag.name)}
              disabled={!mutable || (!tag.reserved && !tag.compliance && !selected.includes(tag.name))}
              onChange={(event) => toggle(tag.name, event.currentTarget.checked)}
            />
          ))}
          {missing.map((tag) => (
            <Checkbox
              key={tag}
              label={`${tag} (no node carries it)`}
              checked={selected.includes(tag)}
              disabled={!mutable}
              onChange={(event) => toggle(tag, event.currentTarget.checked)}
            />
          ))}
        </div>
      ) : (
        <p className="rw-rail-hint">
          No node in {region ? `the ${region} region` : "the cluster"} has a tag yet.
        </p>
      )}
      <p className="rw-rail-hint">
        Log history is not kept, so logs never leave the nodes.
      </p>
      <SettingsFormActions>
        <Button type="submit" disabled={!mutable || busy || !changed}>Save</Button>
      </SettingsFormActions>
    </form>
  );
}
