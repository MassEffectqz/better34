// ESLint flat config: браузерный ESM, строгий поиск неопределённых/неиспользуемых.
import js from '@eslint/js';

export default [
  {
    ignores: ['static/js/dist/**', 'node_modules/**', 'static/js/tests/**'],
  },
  js.configs.recommended,
  {
    files: ['static/js/**/*.js'],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
      globals: {
        window: 'readonly',
        document: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        navigator: 'readonly',
        history: 'readonly',
        location: 'readonly',
        fetch: 'readonly',
        URL: 'readonly',
        Image: 'readonly',
        EventSource: 'readonly',
        IntersectionObserver: 'readonly',
        requestIdleCallback: 'readonly',
        innerWidth: 'readonly',
        innerHeight: 'readonly',
        AbortController: 'readonly',
        FormData: 'readonly',
        FileReader: 'readonly',
        requestAnimationFrame: 'readonly',
        cancelAnimationFrame: 'readonly',
        setTimeout: 'readonly',
        clearTimeout: 'readonly',
        setInterval: 'readonly',
        clearInterval: 'readonly',
        performance: 'readonly',
        CustomEvent: 'readonly',
        console: 'readonly',
      },
    },
    rules: {
      'no-undef': 'error',
      'no-unused-vars': ['error', { args: 'none', caughtErrors: 'none' }],
      'no-empty': ['error', { allowEmptyCatch: true }],
      'no-implicit-globals': 'error',
      eqeqeq: ['error', 'smart'],
      'prefer-const': 'error',
      'no-var': 'error',
    },
  },
];
