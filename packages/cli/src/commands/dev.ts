import { parsarPaths } from '../paths.js'

export function printDevInfo() {
  const paths = parsarPaths()
  console.log('Parsar Phase 0 dev shell')
  console.log(`runtime root: ${paths.root}`)
  console.log('server health: http://127.0.0.1:8080/api/v1/health')
  console.log('web shell: http://127.0.0.1:5173')
}
