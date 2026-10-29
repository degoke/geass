/**
 * Managed mode: a platform that runs this Geass for the people who use it (spec.managed of the platform configuration) keeps
 * five things for itself, and the server refuses a write to any of them. The bootstrap says so in `platform.managed`:
 * `{ enabled, provider, locked }`, where `locked` names the areas. It is absent unless managed mode is on, and then every
 * function here answers as it did before, so a Geass that is not managed shows exactly what it showed.
 */

/** The areas the server can say are managed, as it names them. */
export const MANAGED_AREAS = Object.freeze(["installer", "upgrades", "k3sVersion", "signIn", "dashboardDomain"]);

/** How the pages name the platform when the configuration does not. The server's refusals say the same. */
export const MANAGED_BY_DEFAULT = "the platform that runs this cluster";

// The start of the sentence that says an area is managed. The server's refusal of a write to it says the same, word for word.
const SUBJECTS = Object.freeze({
  installer: "The backup of the cluster's state, which the installer sets up, is",
  upgrades: "Updating Geass is",
  k3sVersion: "The K3s version is",
  signIn: "Sign-in settings are",
  dashboardDomain: "The dashboard's domain and DNS are",
});

/** What the bootstrap says about managed mode, or null when the platform manages nothing. */
export function managedOf(data) {
  const managed = data?.platform?.managed;
  return managed?.enabled ? managed : null;
}

/** The platform's hold on one area, `{ area, by }`, or null when the area is the person's to change. */
export function managedFor(data, area) {
  const managed = managedOf(data);
  if (!managed || !(managed.locked || []).includes(area)) return null;
  return { area, by: managed.provider || MANAGED_BY_DEFAULT };
}

/** Who manages the cluster, for a page that is about more than one area. */
export function managedBy(data) {
  const managed = managedOf(data);
  return managed ? managed.provider || MANAGED_BY_DEFAULT : "";
}

/** The sentence that says an area is managed and cannot be changed, as the server's refusal says it. */
export function managedMessage(managed) {
  return `${SUBJECTS[managed.area] || "This is"} managed by ${managed.by} and cannot be changed here.`;
}
