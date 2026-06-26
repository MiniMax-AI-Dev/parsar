import os from 'node:os'
import path from 'node:path'

export type ParsarPaths = {
  root: string
  config: string
  logs: string
  state: string
  cache: string
  dev: string
}

export function parsarPaths(): ParsarPaths {
  const root = path.join(os.homedir(), '.parsar')
  return {
    root,
    config: path.join(root, 'config'),
    logs: path.join(root, 'logs'),
    state: path.join(root, 'state'),
    cache: path.join(root, 'cache'),
    dev: path.join(root, 'dev'),
  }
}

export function assertAbsoluteOrTilde(input: string): string {
  if (input.startsWith('~/')) {
    return path.join(os.homedir(), input.slice(2))
  }
  if (path.isAbsolute(input)) {
    return path.resolve(input)
  }
  throw new Error('Path must be absolute or start with ~/. Relative paths are not accepted.')
}
