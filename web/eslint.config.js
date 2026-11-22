import js from "@eslint/js";
import pluginQuery from "@tanstack/eslint-plugin-query";
import i18next from "eslint-plugin-i18next";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

// Folders whose text is translated (ADR-0061): a literal string a person
// reads is refused there. Add a folder once it is converted; when every
// folder is, this becomes "src/**".
const translated = ["src/main.tsx", "src/lib/i18n/**"];

// The props of rvav's building blocks and the attributes a person reads or
// hears. Other attributes (className, variant, to, id…) hold no text.
const textAttributes = [
  "aria-label",
  "aria-description",
  "aria-roledescription",
  "aria-valuetext",
  "aria-placeholder",
  "title",
  "placeholder",
  "alt",
  "label",
  "description",
  "hint",
  "body",
  "lead",
  "heading",
  "message",
  "note",
  "empty",
  "error",
  "content",
  "consequences",
  "confirmLabel",
  "cancelLabel",
  "submitLabel",
  "allLabel",
];

// Names that read the same in every language.
const untranslated = [
  "rvav",
  "vRA",
  "vCenter",
  "vSphere",
  "vcsim",
  "OpenAPI",
  "Terraform",
  "PowerShell",
  "Ansible",
  "EfficientIP",
  "Keycloak",
  "GitHub",
  "Git",
  "Teams",
  "Slack",
  "Markdown",
];

export default tseslint.config(
  {
    ignores: [
      "dist",
      "dist-mockup",
      "public/mockServiceWorker.js",
      "src/routeTree.gen.ts",
      "src/lib/api/schema.d.ts",
      "test-results",
      "playwright-report",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommendedTypeChecked,
  ...pluginQuery.configs["flat/recommended"],
  jsxA11y.flatConfigs.recommended,
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      "@typescript-eslint/no-misused-promises": ["error", { checksVoidReturn: { attributes: false } }],
    },
  },
  {
    files: translated,
    ignores: ["**/*.test.ts", "**/*.test.tsx", "src/mocks/**", "src/test/**", "src/lib/api/**"],
    plugins: { i18next },
    rules: {
      "i18next/no-literal-string": [
        "error",
        {
          mode: "jsx-only",
          "should-validate-template": true,
          "jsx-attributes": { include: textAttributes, exclude: [] },
          words: {
            exclude: [/^[^\p{L}]*$/u, "[A-Z][A-Z0-9_-]*s?", ...untranslated, ...untranslated.map((w) => `${w}:`)],
          },
          message: "Text a person reads goes through t() (ADR-0061, docs/frontend.md#translating-a-folder)",
        },
      ],
      "no-restricted-syntax": [
        "error",
        ...[
          "CallExpression[callee.object.name='toast'] > Literal",
          "CallExpression[callee.object.name='toast'] > TemplateLiteral",
          "CallExpression[callee.name='toast'] > Literal",
          "CallExpression[callee.name='toast'] > TemplateLiteral",
          "CallExpression[callee.object.name='toast'] > ObjectExpression > Property[key.name='description'] > Literal",
        ].map((selector) => ({
          selector,
          message: "A toast is read: translate it with t() (ADR-0061)",
        })),
      ],
    },
  },
  {
    files: ["**/*.js", "**/*.mjs"],
    ...tseslint.configs.disableTypeChecked,
  },
);
