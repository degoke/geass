import { useComputedColorScheme } from "@mantine/core";
import { useDashboardOptions } from "@/lib/options";

/** The wordmark, or the symbol, in the variant that reads on the current theme. */
export function GeassLogo({ symbol = false, alt = "Geass", ...props }) {
  const { logos } = useDashboardOptions();
  const dark = useComputedColorScheme("light", { getInitialValueInEffect: false }) === "dark";
  const file = symbol
    ? dark ? "geass-symbol" : "geass-symbol-light"
    : dark ? "geass-logo-dark" : "geass-logo-light";
  // Mounted in another app the files are not at the root of the page, so the library brings its own.
  return <img src={logos?.[file] || `/${file}.svg`} alt={alt} {...props} />;
}
