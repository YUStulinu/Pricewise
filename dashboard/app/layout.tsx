import type { Metadata } from 'next'
import Link from 'next/link'
import { overview } from '@/lib/queries'
import './globals.css'

export const metadata: Metadata = {
  title: 'Pricewise',
  description: 'Prices of the same product across shops, with fake discounts flagged',
}

export const dynamic = 'force-dynamic'

export default async function Layout({ children }: { children: React.ReactNode }) {
  let info: Awaited<ReturnType<typeof overview>> | null = null
  try {
    info = await overview()
  } catch {
    info = null
  }
  return (
    <html lang="en">
      <body>
        <header>
          <Link href="/" className="brand">Pricewise</Link>
          <nav>
            <Link href="/">Products</Link>
            <Link href="/discounts">Suspicious discounts</Link>
            <Link href="/alerts">Alerts</Link>
          </nav>
        </header>
        {info?.simulated && (
          <div className="notice">
            Simulated shops: the product titles are real offers from the WDC Products benchmark, but the shops and
            every price are simulated, with fake discounts written in on purpose to test the detector.
          </div>
        )}
        {!info && <div className="notice bad">The database is not answering. Start it with docker compose up -d.</div>}
        <main>{children}</main>
        {info && (
          <footer>
            {info.products} products · {info.offers} offers in {info.shops} shops · {info.observations.toLocaleString('en')}{' '}
            prices from {info.first} to {info.last}
          </footer>
        )}
      </body>
    </html>
  )
}
