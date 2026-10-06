import { createTheme } from "@mantine/core";

const signal = [
  "#f4f4fe",
  "#e9e9fd",
  "#d3d3fb",
  "#bdbdfb",
  "#a7a7f9",
  "#888cf8",
  "#6d6ce6",
  "#5453c4",
  "#3c3ba2",
  "#292880",
];

export const geassTheme = createTheme({
  primaryColor: "signal",
  primaryShade: 5,
  colors: { signal },
  black: "#f2f2f3",
  white: "#121214",
  defaultRadius: 8,
  fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif",
  fontFamilyMonospace: '"Geist Mono Variable", "Geist Mono", ui-monospace, SFMono-Regular, Menlo, monospace',
  fontSizes: {
    xs: "12px",
    sm: "14px",
    md: "14px",
    lg: "16px",
    xl: "20px",
  },
  lineHeights: { xs: "1.4", sm: "1.45", md: "1.45", lg: "1.4", xl: "1.3" },
  headings: {
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif",
    fontWeight: "600",
    sizes: {
      h1: { fontSize: "22px", fontWeight: "600", lineHeight: "1.3" },
      h2: { fontSize: "16px", fontWeight: "600", lineHeight: "1.35" },
      h3: { fontSize: "14px", fontWeight: "600", lineHeight: "1.4" },
      h4: { fontSize: "14px", fontWeight: "600", lineHeight: "1.4" },
    },
  },
  components: {
    Button: { defaultProps: { radius: 6, size: "sm", fw: 500 } },
    Paper: { defaultProps: { radius: 8, shadow: "none" } },
    TextInput: { defaultProps: { radius: 6, size: "sm" } },
    PasswordInput: { defaultProps: { radius: 6, size: "sm" } },
    NativeSelect: { defaultProps: { radius: 6, size: "sm" } },
    Select: { defaultProps: { radius: 6, size: "sm" } },
    Textarea: { defaultProps: { radius: 6, size: "sm" } },
  },
});
