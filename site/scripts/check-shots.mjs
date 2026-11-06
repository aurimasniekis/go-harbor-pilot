// Fails when a page shows a screenshot that does not exist (both the -light
// and -dark capture, or -mobile for phone shots) or has no descriptive alt
// text, when a page uses a screenshot that is missing from the inventory
// (docs/SCREENSHOTS.md), or when the inventory marks a screenshot current but
// its files are missing, or lists a screenshot that no page uses. Regenerate
// screenshots with `make screenshots`.
import fs from 'node:fs';
import path from 'node:path';

const site = path.resolve(import.meta.dirname, '..');
const root = path.resolve(site, '..');
const shots = path.join(site, 'public', 'screenshots');
const inventory = fs.readFileSync(path.join(root, 'docs', 'SCREENSHOTS.md'), 'utf8');
const rows = new Map();
for (const m of inventory.matchAll(/^\| `([a-z0-9-]+)` \|[^|]*\|[^|]*\| (\w+)/gm)) rows.set(m[1], m[2]);
const bad = [];
const used = new Set();
const has = (f) => fs.existsSync(path.join(shots, f));
const exists = (name) => has(`${name}-light.png`) && has(`${name}-dark.png`);
const walk = (d) => {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name.startsWith('.') || e.name === 'public') continue;
    const p = path.join(d, e.name);
    if (e.isDirectory()) walk(p);
    else if (p.endsWith('.md')) {
      for (const m of fs.readFileSync(p, 'utf8').matchAll(/<Shot[^>]*\bname="([^"]+)"[^>]*>/g)) {
        const rel = path.relative(site, p);
        const mobile = /\bmobile\b/.test(m[0]);
        const files = mobile ? [`${m[1]}-mobile.png`] : [`${m[1]}-light.png`, `${m[1]}-dark.png`];
        used.add(m[1]);
        if (!/\balt="[^"]{10,}"/.test(m[0])) bad.push(`${rel}: <Shot name="${m[1]}"> needs descriptive alt text`);
        for (const f of files) if (!has(f)) bad.push(`${rel}: screenshot ${f} is missing (run make screenshots)`);
        if (!rows.has(m[1])) bad.push(`${rel}: screenshot ${m[1]} is not in docs/SCREENSHOTS.md`);
      }
    }
  }
};
walk(site);
for (const [name, status] of rows) {
  if (status === 'current' && !exists(name)) bad.push(`docs/SCREENSHOTS.md: ${name} is current but missing`);
  if (!used.has(name)) bad.push(`docs/SCREENSHOTS.md: ${name} is not shown on any page`);
}
if (bad.length) {
  console.error(bad.join('\n'));
  process.exit(1);
}
console.log(`screenshots ok (${rows.size} inventoried)`);
