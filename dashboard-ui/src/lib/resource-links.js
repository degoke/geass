import { resourceName } from "@/lib/api";
import { isObjectStoreEngine } from "@/lib/object-storage";

export function appHref(project, name, environment) {
  return `/projects/${project}/apps/${name}?environment=${environment}`;
}

export function resourceHref(project, environment, item, kind) {
  const slug = typeof project === "string" ? project : resourceName(project);
  const name = resourceName(item);
  const engine = item.spec?.engine;
  if (kind === "Service") return `/projects/${slug}/apps/${name}?environment=${environment}`;
  if (kind === "Logical DB") return `/projects/${slug}/logical-databases/${name}?environment=${environment}`;
  if (engine || kind === "Database" || kind === "Bucket" || kind === "Redis") {
    const segment = isObjectStoreEngine(engine) ? "object-stores" : "databases";
    return `/projects/${slug}/${segment}/${name}?environment=${environment}`;
  }
  return `/projects/${slug}`;
}
