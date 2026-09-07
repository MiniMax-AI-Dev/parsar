import { createMDX } from "fumadocs-mdx/next"

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  // Keep the Next.js development overlay indicator out of the product UI.
  devIndicators: false,
}

export default withMDX(config)
