import fs from 'node:fs/promises'
import path from 'node:path'
import { parsarPaths } from '../paths.js'

const seed = {
  workspace: { id: 'dev_workspace', name: 'Demo Workspace', slug: 'demo' },
  users: [{ id: 'dev_admin', email: 'admin@example.com', name: 'Dev Admin', role: 'owner' }],
  agents: [
    { id: 'agent_product', name: '产品Agent', slug: 'product-agent' },
    { id: 'agent_backend', name: '后端Agent', slug: 'backend-agent' },
    { id: 'agent_test', name: '测试Agent', slug: 'test-agent' },
  ],
  conversations: [{ id: 'conv_demo_group', title: 'Demo Group', visibility: 'workspace' }],
}

export async function writeDevSeed() {
  const paths = parsarPaths()
  const seedDir = path.join(paths.dev, 'seed')
  await fs.mkdir(seedDir, { recursive: true })
  const seedPath = path.join(seedDir, 'seed.json')
  await fs.writeFile(seedPath, JSON.stringify(seed, null, 2) + '\n')
  console.log(`Wrote Parsar dev seed to ${seedPath}`)
}
