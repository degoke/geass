import { AppLink } from "@/components/AppLink";

/** Full-width account strip (Railway-style trial / setup bar). */
export function StripBanner({ lead, message, href, actionLabel }) {
  return (
    <div className="rw-strip-banner" role="status">
      <p className="rw-strip-banner-copy">
        {lead ? <span className="rw-strip-banner-lead">{lead}</span> : null}
        {lead && message ? <span className="rw-strip-banner-sep" aria-hidden="true">|</span> : null}
        {message ? <span className="rw-strip-banner-message">{message}</span> : null}
      </p>
      {href && actionLabel ? (
        <AppLink href={href} className="rw-strip-banner-action">{actionLabel}</AppLink>
      ) : null}
    </div>
  );
}
