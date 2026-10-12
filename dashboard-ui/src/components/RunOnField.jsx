import { tagsInRegion } from "@/lib/placement";
import { Checkbox, Muted } from "@/components/geass-ui";
import { ResourceSettingsBlock } from "@/layout/ResourceSettingsLayout";
import { SettingsPanel } from "@/layout/SettingsShell";

/**
 * Choose which kinds of node a service or database runs on. It only appears once nodes carry tags, so
 * clusters that never use them see nothing. `value` is the chosen tag names.
 */
export function RunOnField({ data, region, value, onChange, disabled = false, note }) {
  const available = tagsInRegion(data, region);
  // A tag the resource still asks for but no node carries any more must stay visible, or it could
  // never be unchecked.
  const orphaned = value.filter((name) => !available.some((tag) => tag.name === name));
  if (!available.length && !orphaned.length) return null;

  const toggle = (name, on) => onChange(on ? [...value.filter((tag) => tag !== name), name].sort() : value.filter((tag) => tag !== name));
  const where = region ? `in the ${region} region` : "in the cluster";

  return (
    <ResourceSettingsBlock
      title="Run on"
      description={`Only use nodes with these tags. With none chosen, any node ${where} can be used.`}
    >
      <SettingsPanel>
        {available.map((tag) => (
          <Checkbox
            key={tag.name}
            label={tag.reserved ? `${tag.name} (reserved: only for work that asks for it)` : tag.name}
            description={`${tag.nodes} ${tag.nodes === 1 ? "node" : "nodes"} ${where}`}
            checked={value.includes(tag.name)}
            disabled={disabled}
            onChange={(event) => toggle(tag.name, event.currentTarget.checked)}
          />
        ))}
        {orphaned.map((name) => (
          <Checkbox
            key={name}
            label={`${name} (no node has this tag now)`}
            checked
            disabled={disabled}
            onChange={() => toggle(name, false)}
          />
        ))}
        {note ? <Muted size="xs">{note}</Muted> : null}
      </SettingsPanel>
    </ResourceSettingsBlock>
  );
}
