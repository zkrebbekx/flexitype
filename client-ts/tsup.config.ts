import { defineConfig } from 'tsup'

// Two entry points, both ESM and CJS, both with declarations.
//
// The core entry must stay free of React: the Vue console is expected to adopt
// it, so `react` and `@tanstack/react-query` are external and are imported only
// from src/react.
export default defineConfig({
  entry: {
    index: 'src/index.ts',
    'react/index': 'src/react/index.ts',
  },
  format: ['esm', 'cjs'],
  dts: true,
  sourcemap: true,
  clean: true,
  treeshake: true,
  target: 'es2022',
  external: ['react', 'react/jsx-runtime', '@tanstack/react-query'],
})
