/** @type {import('tailwindcss').Config} */
//
// Semantic color tokens — every component should reach for one of these
// (`bg-surface-2`, `text-fg-muted`, `border-border`) instead of hardcoding
// a Tailwind palette name. Light/dark values live in src/index.css as
// CSS variables; switching theme is a one-class toggle on <html>.
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        surface:          "rgb(var(--color-surface) / <alpha-value>)",
        "surface-2":      "rgb(var(--color-surface-2) / <alpha-value>)",
        "surface-3":      "rgb(var(--color-surface-3) / <alpha-value>)",
        fg:               "rgb(var(--color-fg) / <alpha-value>)",
        "fg-muted":       "rgb(var(--color-fg-muted) / <alpha-value>)",
        "fg-subtle":      "rgb(var(--color-fg-subtle) / <alpha-value>)",
        border:           "rgb(var(--color-border) / <alpha-value>)",
        "border-strong":  "rgb(var(--color-border-strong) / <alpha-value>)",
        accent: {
          DEFAULT: "rgb(var(--color-accent) / <alpha-value>)",
          hover:   "rgb(var(--color-accent-hover) / <alpha-value>)",
          fg:      "rgb(var(--color-accent-fg) / <alpha-value>)",
        },
      },
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        card: "0 1px 2px 0 rgb(0 0 0 / 0.04), 0 1px 4px 0 rgb(0 0 0 / 0.04)",
        "card-dark": "0 1px 2px 0 rgb(0 0 0 / 0.4), 0 1px 4px 0 rgb(0 0 0 / 0.2)",
      },
    },
  },
  plugins: [],
};
