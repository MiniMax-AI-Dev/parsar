import { parsarPaths } from '../paths.js'

export function runDoctor() {
  const paths = parsarPaths()
  console.log('Parsar doctor')
  console.log(`config: ${paths.config}`)
  console.log(`logs: ${paths.logs}`)
  console.log(`state: ${paths.state}`)
  console.log(`cache: ${paths.cache}`)
}
