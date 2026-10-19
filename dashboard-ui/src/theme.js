import { createTheme } from "@mantine/core";

// "signal" is C.C. green. Shade 5 (#7ce0a0) is the brand accent on dark surfaces; shade 7 (#16935a) is the deep green used on light.
const signal = [
  "#effcf4",
  "#d9f8e5",
  "#bff1d2",
  "#a6ebbf",
  "#90e5b0",
  "#7ce0a0",
  "#5fc487",
  "#16935a",
  "#2f8352",
  "#1d5f39",
];

const sans = '"Space Grotesk Variable", "Space Grotesk", ui-sans-serif, system-ui, sans-serif';
const mono = '"JetBrains Mono Variable", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

export const geassTheme = createTheme({
  primaryColor: "signal",
  primaryShade: { light: 7, dark: 5 },
  // Shade 5 is a light colour, so Mantine must pick dark text for filled controls on dark; shade 7 gets white.
  autoContrast: true,
  colors: { signal },
  black: "#0a0a0a",
  // The Contract is square-cornered: every radius token resolves to 0.
  defaultRadius: 0,
  radius: { xs: "0", sm: "0", md: "0", lg: "0", xl: "0" },
  fontFamily: sans,
  fontFamilyMonospace: mono,
  fontSizes: {
    xs: "12px",
    sm: "14px",
    md: "14px",
    lg: "16px",
    xl: "20px",
  },
  lineHeights: { xs: "1.4", sm: "1.45", md: "1.45", lg: "1.4", xl: "1.3" },
  headings: {
    fontFamily: sans,
    fontWeight: "600",
    sizes: {
      h1: { fontSize: "22px", fontWeight: "600", lineHeight: "1.3" },
      h2: { fontSize: "16px", fontWeight: "600", lineHeight: "1.35" },
      h3: { fontSize: "14px", fontWeight: "600", lineHeight: "1.4" },
      h4: { fontSize: "14px", fontWeight: "600", lineHeight: "1.4" },
    },
  },
  components: {
    Modal: {
      // Above in-canvas resource / deployment drawers (z-index ~200–202).
      defaultProps: { zIndex: 450 },
    },
    Button: { defaultProps: { radius: 0, size: "sm", fw: 500 } },
    Paper: { defaultProps: { radius: 0, shadow: "none" } },
    TextInput: { defaultProps: { radius: 0, size: "sm" } },
    PasswordInput: { defaultProps: { radius: 0, size: "sm" } },
    NativeSelect: { defaultProps: { radius: 0, size: "sm" } },
    Select: { defaultProps: { radius: 0, size: "sm" } },
    Textarea: { defaultProps: { radius: 0, size: "sm" } },
  },
});
