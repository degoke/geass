import { Link } from "@tanstack/react-router";

export function PageSubnav({ items, activeId, ariaLabel = "Section" }) {
  if (!items?.length) return null;
  return (
    <nav className="geass-section-nav" aria-label={ariaLabel}>
      {items.map((item) => (
        <Link
          key={item.id}
          to={item.to}
          search={item.search}
          className={`geass-section-nav__link${activeId === item.id ? " geass-section-nav__link--active" : ""}`}
          role="tab"
          aria-selected={activeId === item.id}
        >
          {item.label}
        </Link>
      ))}
    </nav>
  );
}
