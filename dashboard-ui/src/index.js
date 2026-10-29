// The entry point of the library build (see vite.lib.config.js): what another app imports from "@geass/dashboard-ui".
import "@mantine/code-highlight/styles.css";
import "./styles.css";
import "./embedded.css";

export { GeassDashboard } from "./GeassDashboard";
export { normalizeBasePath } from "./lib/base-path";
export { geassTheme } from "./theme";
export { HIDEABLE, SELF_HOSTED_ONLY } from "./lib/options";
export { ApiError } from "./lib/api";
