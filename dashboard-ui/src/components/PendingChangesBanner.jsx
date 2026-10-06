import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { pendingChangeCopy } from "@/lib/services";
import { Button, PendingBanner } from "@/components/geass-ui";

export function PendingChangesBanner({ item, name, reload, data }) {
  const copy = pendingChangeCopy(item);
  if (!copy) return null;
  return (
    <PendingBanner
      message={copy.message}
      action={canMutate(data) ? (
        <Button size="sm" onClick={() => action(`/apps/${name}/deploy`).then(() => { reload(); alertUser("Deploy requested"); }).catch((error) => alertUser(error.message))}>
          {copy.action}
        </Button>
      ) : null}
    />
  );
}
