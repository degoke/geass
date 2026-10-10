import { ResourceRowLink } from "@/components/geass-ui";
import { resourceDisplayName } from "@/lib/api";
import { resourceStatus } from "@/lib/services";
import { Status } from "@/components/Status";

export function ResourceRow({ href, icon, item, kind }) {
  return (
    <ResourceRowLink
      href={href}
      icon={icon}
      title={resourceDisplayName(item)}
      subtitle={`${kind} · ${item.spec?.environment}`}
      status={<Status value={resourceStatus(item)} />}
    />
  );
}
