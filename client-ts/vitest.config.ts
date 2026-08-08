import { defineConfig } from 'vitest/config'

// happy-dom and globals match web/, so a contributor moving between the two
// packages meets one test setup rather than two.
export default defineConfig({
  test: {
    environment: 'happy-dom',
    globals: true,
    include: ['test/**/*.test.ts', 'test/**/*.test.tsx'],
  },
})
