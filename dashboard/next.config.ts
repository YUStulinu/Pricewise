import type { NextConfig } from 'next'

const config: NextConfig = {
  // pg is a Node library; keep it out of the bundle.
  serverExternalPackages: ['pg'],
}

export default config
