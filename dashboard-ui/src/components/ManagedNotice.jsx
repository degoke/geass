import { Badge, Muted } from "@/components/geass-ui";
import { managedMessage } from "@/lib/managed";

/** Says that the platform that runs this Geass keeps an area for itself. Nothing when `managed` is null. */
export function ManagedNotice({ managed }) {
  if (!managed) return null;
  return (
    <div className="geass-managed-notice" role="status" data-managed-area={managed.area}>
      <Badge>Managed by {managed.by}</Badge>
      <Muted size="sm">{managedMessage(managed)}</Muted>
    </div>
  );
}

/**
 * What an area shows when the platform keeps it: the page as it is, with the reason above it, and every field and button
 * under it off (a disabled fieldset turns off the controls inside it, and the server refuses what they would send). Just the
 * children when `managed` is null.
 */
export function ManagedArea({ managed, children }) {
  if (!managed) return children;
  return (
    <>
      <ManagedNotice managed={managed} />
      <fieldset className="geass-managed-fieldset" disabled>
        {children}
      </fieldset>
    </>
  );
}
