import { Link } from "@tanstack/react-router";

export function AppLink({ href, ...props }) {
  return <Link to={href} {...props} />;
}
