import { describe, expect, it } from 'vitest'
import { assertAbsoluteOrTilde, parsarPaths } from './paths.js'

describe('parsar paths', () => {
  it('uses ~/.parsar as runtime root', () => {
    expect(parsarPaths().root).toContain('.parsar')
  })

  it('rejects relative paths', () => {
    expect(() => assertAbsoluteOrTilde('relative/path')).toThrow(/absolute/)
  })
})
