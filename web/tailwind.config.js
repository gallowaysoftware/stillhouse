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
        surface:          "rgb(var(--sh-surface) / <alpha-value>)",
        "surface-2":      "rgb(var(--sh-surface-2) / <alpha-value>)",
        "surface-3":      "rgb(var(--sh-surface-3) / <alpha-value>)",
        fg:               "rgb(var(--sh-fg) / <alpha-value>)",
        "fg-muted":       "rgb(var(--sh-fg-muted) / <alpha-value>)",
        "fg-subtle":      "rgb(var(--sh-fg-subtle) / <alpha-value>)",
        border:           "rgb(var(--sh-border) / <alpha-value>)",
        "border-strong":  "rgb(var(--sh-border-strong) / <alpha-value>)",
        accent: {
          DEFAULT: "rgb(var(--sh-accent) / <alpha-value>)",
          hover:   "rgb(var(--sh-accent-hover) / <alpha-value>)",
          fg:      "rgb(var(--sh-accent-fg) / <alpha-value>)",
        },
        // Semantic state colors — kept distinct from accent so the eye can
        // tell at a glance: amber = warning (something needs attention),
        // emerald = success/ok, red = danger/destructive, sky = info.
        // Components should reach for these, not raw Tailwind palette names.
        success: {
          DEFAULT: "rgb(var(--sh-success) / <alpha-value>)",
          fg:      "rgb(var(--sh-success-fg) / <alpha-value>)",
        },
        warning: {
          DEFAULT: "rgb(var(--sh-warning) / <alpha-value>)",
          fg:      "rgb(var(--sh-warning-fg) / <alpha-value>)",
        },
        danger: {
          DEFAULT: "rgb(var(--sh-danger) / <alpha-value>)",
          fg:      "rgb(var(--sh-danger-fg) / <alpha-value>)",
        },
        info: {
          DEFAULT: "rgb(var(--sh-info) / <alpha-value>)",
          fg:      "rgb(var(--sh-info-fg) / <alpha-value>)",
        },
        // Categorical chart series. Deliberately separate from the state
        // colors above — a series is not a status.
        "series-1": "rgb(var(--sh-series-1) / <alpha-value>)",
        "series-2": "rgb(var(--sh-series-2) / <alpha-value>)",
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
