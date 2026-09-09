import { createMDX } from "fumadocs-mdx/next"
import { fileURLToPath } from "node:url"

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  outputFileTracingRoot: fileURLToPath(new URL("../../", import.meta.url)),
  // Keep the Next.js development overlay indicator out of the product UI.
  devIndicators: false,
}

export default withMDX(config)
