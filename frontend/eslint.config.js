import js from '@eslint/js'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import globals from 'globals'
import tseslint from 'typescript-eslint'

// Physical left/right utilities break RTL; use ms-/me-/ps-/pe-/start-/end- (ARCHITECTURE §3.4).
const physicalClass =
  /(^|[\s:"'`])-?(ml|mr|pl|pr|left|right|border-l|border-r|rounded-l|rounded-r|rounded-tl|rounded-tr|rounded-bl|rounded-br|scroll-ml|scroll-mr|scroll-pl|scroll-pr)-|(^|[\s:"'`])(text-left|text-right|float-left|float-right|clear-left|clear-right)($|[\s"'`])/
const rtlMessage = 'Use logical utilities (ms-/me-/ps-/pe-/start-/end-/text-start/text-end) instead of left/right.'

const adminImport = {
  group: ['@/app/admin/*', '@/features/admin/*', '**/app/admin/*', '**/features/admin/*'],
  message: 'Customer and shared code must not import staff code (separate bundles, SECURITY §11).',
}

export default tseslint.config(
  { ignores: ['dist', 'dev-dist', 'node_modules'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended, reactHooks.configs.flat.recommended],
    languageOptions: { ecmaVersion: 2023, globals: globals.browser },
    plugins: { 'react-refresh': reactRefresh },
    rules: {
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      'no-restricted-syntax': [
        'error',
        { selector: `Literal[value=${physicalClass}]`, message: rtlMessage },
        { selector: `TemplateElement[value.raw=${physicalClass}]`, message: rtlMessage },
      ],
    },
  },
  {
    // Everything except the staff app itself.
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/app/admin/**', 'src/features/admin/**'],
    rules: { 'no-restricted-imports': ['error', { patterns: [adminImport] }] },
  },
  {
    // shadcn/ui files export variants next to components.
    files: ['src/components/ui/**'],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
  {
    files: ['*.config.ts', 'scripts/**/*.mjs'],
    languageOptions: { globals: globals.node },
  },
)
