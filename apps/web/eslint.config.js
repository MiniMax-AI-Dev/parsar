import js from "@eslint/js"
import globals from "globals"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import tseslint from "typescript-eslint"

/**
 * ESLint config — strict but pragmatic. Two custom guards keep design-system
 * discipline; the rest is the standard React + TS preset.
 */
export default tseslint.config(
  { ignores: ["dist", "node_modules"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],

      /* ------------------------------------------------------------
       * Design-system guards. Catch raw palette / pixel sizes before
       * they land. Errors are intentional — fix at the source.
       * ---------------------------------------------------------- */
      "no-restricted-syntax": [
        "error",
        {
          // Literal "text-[12px]" / "text-[20px]" etc.
          selector:
            "Literal[value=/(?:^|\\s)text-\\[\\d+px\\](?:\\s|$)/]",
          message:
            "Arbitrary font sizes are banned. Use one of: text-xs, text-sm, text-base, text-lg, text-2xl.",
        },
        {
          // Same, inside template literals.
          selector:
            "TemplateElement[value.cooked=/(?:^|\\s)text-\\[\\d+px\\](?:\\s|$)/]",
          message:
            "Arbitrary font sizes are banned. Use one of: text-xs, text-sm, text-base, text-lg, text-2xl.",
        },
        {
          // Raw palette: text-slate-500, bg-red-50, border-emerald-200, …
          selector:
            "Literal[value=/(?:^|\\s)(?:text|bg|border)-(?:slate|red|amber|emerald|blue|green|rose|indigo|sky|purple|pink|orange|teal|cyan|lime|yellow|violet)-\\d+(?:\\/\\d+)?(?:\\s|$)/]",
          message:
            "Raw Tailwind palette is banned. Use semantic tokens: text-fg / text-fg-muted / text-danger / bg-surface-subtle / border-line / etc. See src/style.css @theme block.",
        },
        {
          selector:
            "TemplateElement[value.cooked=/(?:^|\\s)(?:text|bg|border)-(?:slate|red|amber|emerald|blue|green|rose|indigo|sky|purple|pink|orange|teal|cyan|lime|yellow|violet)-\\d+(?:\\/\\d+)?(?:\\s|$)/]",
          message:
            "Raw Tailwind palette is banned. Use semantic tokens: text-fg / text-fg-muted / text-danger / bg-surface-subtle / border-line / etc. See src/style.css @theme block.",
        },
      ],
    },
  },
)
