import { useState } from "react";
import { action, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
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
      .then(() => { reload(); alertUser("Compliance settings saved"); })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <form className="rw-rail-form" onSubmit={save}>
      <p className="rw-rail-body">
        Choose the node tags this project requires. Everything it runs stays on nodes that carry all of them,
        and those nodes accept nothing else. Services roll onto the nodes at once; databases keep their data
        where it is, so a project that already has databases has to restore them into new ones first.
      </p>
      {candidates.length || missing.length ? (
        <div className="rw-rail-stack">
          {candidates.map((tag) => (
            <Checkbox
              key={tag.name}
              label={`${tag.name} (${tag.nodes} ${tag.nodes === 1 ? "node" : "nodes"}${tag.compliance ? ", compliance" : tag.reserved ? ", reserved" : ""})`}
              description={tag.reserved || tag.compliance ? undefined : "Mark this tag as a compliance tag on its nodes first."}
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
          No node in {region ? `the ${region} region` : "the cluster"} has a tag yet. Add a compliance tag to a node in
          Cluster capacity first.
        </p>
      )}
      <p className="rw-rail-hint">
        Log history is not kept for projects that require tags, so their logs never leave the nodes they run on.
        Live logs still work.
      </p>
      <SettingsFormActions>
        <Button type="submit" disabled={!mutable || busy || !changed}>Save</Button>
      </SettingsFormActions>
    </form>
  );
}
