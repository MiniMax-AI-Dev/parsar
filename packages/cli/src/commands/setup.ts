import fs from 'node:fs/promises'
import { parsarPaths } from '../paths.js'

export async function runSetup() {
  const paths = parsarPaths()
  await Promise.all(Object.values(paths).map((dir) => fs.mkdir(dir, { recursive: true })))
  console.log('Parsar local directories are ready:')
  for (const [key, value] of Object.entries(paths)) {
    console.log(`${key}: ${value}`)
  }
}
