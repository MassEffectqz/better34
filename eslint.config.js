// ESLint flat config: browser ESM plus a separate Node/test environment.
import js from '@eslint/js';

const browserGlobals = {
  window: 'readonly', document: 'readonly', localStorage: 'readonly', sessionStorage: 'readonly',
  navigator: 'readonly', history: 'readonly', location: 'readonly', fetch: 'readonly', URL: 'readonly',
  Image: 'readonly', ImageData: 'readonly', URLSearchParams: 'readonly', caches: 'readonly', indexedDB: 'readonly',
  EventSource: 'readonly', IntersectionObserver: 'readonly', requestIdleCallback: 'readonly',
  innerWidth: 'readonly', innerHeight: 'readonly', AbortController: 'readonly', FormData: 'readonly',
  FileReader: 'readonly', requestAnimationFrame: 'readonly', cancelAnimationFrame: 'readonly',
  setTimeout: 'readonly', clearTimeout: 'readonly', setInterval: 'readonly', clearInterval: 'readonly',
  performance: 'readonly', CustomEvent: 'readonly', DOMParser: 'readonly', console: 'readonly',
  queueMicrotask: 'readonly', BarcodeDetector: 'readonly', Notification: 'readonly',
};

const rules = {
  'no-undef': 'error',
  'no-unused-vars': ['error', { args: 'none', caughtErrors: 'none' }],
  'no-empty': ['error', { allowEmptyCatch: true }],
  'no-eval': 'error',
  'no-new-func': 'error',
  'no-implicit-globals': 'error',
  eqeqeq: ['error', 'smart'],
  'prefer-const': 'error',
  'no-var': 'error',
};

export default [
  { ignores: ['static/js/dist/**', 'node_modules/**'] },
  js.configs.recommended,
  {
    files: ['static/js/**/*.js', '!static/js/tests/**/*.js'],
    languageOptions: { ecmaVersion: 2022, sourceType: 'module', globals: browserGlobals },
    rules,
  },
  {
    files: ['static/js/tests/**/*.js'],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
      globals: {
        ...browserGlobals,
        process: 'readonly', Buffer: 'readonly', setImmediate: 'readonly', clearImmediate: 'readonly',
      },
    },
    rules,
  },
];
