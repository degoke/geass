import { useComputedColorScheme } from "@mantine/core";

/** The wordmark, or the symbol, in the variant that reads on the current theme. */
export function GeassLogo({ symbol = false, alt = "Geass", ...props }) {
  const dark = useComputedColorScheme("light", { getInitialValueInEffect: false }) === "dark";
  const file = symbol
    ? dark ? "geass-symbol" : "geass-symbol-light"
    : dark ? "geass-logo-dark" : "geass-logo-light";
  return <img src={`/${file}.svg`} alt={alt} {...props} />;
}
