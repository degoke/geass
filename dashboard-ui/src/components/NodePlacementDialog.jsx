import { useEffect, useState } from "react";
import { Modal, Stack, Text } from "@mantine/core";
import { action } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { parseTagList, platformRegions, platformTags } from "@/lib/placement";
import { Button, Field, Input, Muted, Switch } from "@/components/geass-ui";

/**
 * Edits where a node sits (its region and zone) and what it is for (its tags). A reserved tag keeps
 * every workload that does not ask for it off the node, so the node is dedicated to the work that does.
 * Running workloads are never moved; a workload that restarts only returns to nodes that match.
 */
export function NodePlacementDialog({ node, data, reload, onClose }) {
  const [region, setRegion] = useState("");
  const [zone, setZone] = useState("");
  const [tagText, setTagText] = useState("");
  const [reserved, setReserved] = useState([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setRegion(node?.region || "");
    setZone(node?.zone || "");
    setTagText((node?.tags || []).map((tag) => tag.name).join(", "));
    setReserved((node?.tags || []).filter((tag) => tag.reserved).map((tag) => tag.name));
  }, [node]);

  const tags = parseTagList(tagText);
  const knownRegions = platformRegions(data).map((entry) => entry.name);
  const knownTags = platformTags(data).map((entry) => entry.name).filter((name) => !tags.includes(name));

  const toggleReserved = (name, on) => {
    setReserved((current) => (on ? [...current.filter((tag) => tag !== name), name] : current.filter((tag) => tag !== name)));
  };

  const submit = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      const result = await action(`/nodes/${node.name}/placement`, {
        region: region.trim(),
        zone: zone.trim(),
        tags: tags.join(","),
        reserved: reserved.filter((name) => tags.includes(name)).join(","),
      });
      await reload?.();
      (result?.warnings || []).forEach((warning) => alertUser(warning));
      alertUser("Node updated");
      onClose();
    } catch (error) {
      alertUser(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={Boolean(node)} onClose={() => { if (!busy) onClose(); }} title={node ? `Edit ${node.name}` : ""} centered>
      {node ? (
        <form onSubmit={submit}>
          <Stack gap="sm">
            <Field label="Region" description="Projects pinned to a region only run on nodes in it. Leave empty for no region.">
              <Input
                value={region}
                onChange={(event) => setRegion(event.currentTarget.value)}
                list="geass-known-regions"
                placeholder="us"
                aria-label="Region"
              />
              <datalist id="geass-known-regions">
                {knownRegions.map((name) => <option key={name} value={name} />)}
              </datalist>
            </Field>
            <Field label="Site" description="The provider or site inside the region, such as aws-us-east-1.">
              <Input value={zone} onChange={(event) => setZone(event.currentTarget.value)} placeholder="aws-us-east-1" aria-label="Site" />
            </Field>
            <Field label="Tags" description="What this node is for, separated by commas: storage, compute, gpu.">
              <Input value={tagText} onChange={(event) => setTagText(event.currentTarget.value)} placeholder="storage, compute" aria-label="Tags" />
            </Field>
            {knownTags.length ? <Muted size="xs">Tags in use elsewhere: {knownTags.join(", ")}</Muted> : null}
            {tags.length ? (
              <Stack gap={6}>
                <Text size="sm" fw={500}>Reserve for a tag</Text>
                <Muted size="xs">
                  A reserved node only runs workloads that ask for that tag. Everything else stays off it.
                </Muted>
                {tags.map((name) => (
                  <Switch
                    key={name}
                    label={`Reserve this node for ${name}`}
                    checked={reserved.includes(name)}
                    onChange={(event) => toggleReserved(name, event.currentTarget.checked)}
                  />
                ))}
              </Stack>
            ) : null}
            <Muted size="xs">
              Workloads that are running are not moved. One that restarts only returns to a node that matches.
            </Muted>
            <Button type="submit" loading={busy}>Save</Button>
          </Stack>
        </form>
      ) : null}
    </Modal>
  );
}
