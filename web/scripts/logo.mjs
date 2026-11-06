/* global process */
// Renders site/public/logo.svg to PNG sizes for favicons and social cards:
//   node web/scripts/logo.mjs site/public/logo.svg site/public/logo
import { chromium } from '@playwright/test'
import { readFileSync } from 'node:fs'
const [svgPath, outDir] = process.argv.slice(2)
const svg = readFileSync(svgPath, 'utf8')
const b = await chromium.launch()
for (const size of [32, 64, 128, 256, 512]) {
  const p = await b.newPage({ viewport: { width: size, height: size } })
  await p.setContent(`<html><body style="margin:0;background:transparent">${svg.replace('<svg ', `<svg width="${size}" height="${size}" `)}</body></html>`)
  await p.screenshot({ path: `${outDir}/logo-${size}.png`, omitBackground: true })
  await p.close()
}
await b.close()
