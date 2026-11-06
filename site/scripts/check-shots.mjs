// Fails when a page shows a screenshot that does not exist (both the -light
// and -dark capture, or -mobile for phone shots) or has no descriptive alt
// text. Regenerate screenshots with `make screenshots`.
import fs from 'node:fs';
import path from 'node:path';

const site = path.resolve(import.meta.dirname, '..');
const shots = path.join(site, 'public', 'screenshots');
const bad = [];
const has = (f) => fs.existsSync(path.join(shots, f));
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
        if (!/\balt="[^"]{10,}"/.test(m[0])) bad.push(`${rel}: <Shot name="${m[1]}"> needs descriptive alt text`);
        for (const f of files) if (!has(f)) bad.push(`${rel}: screenshot ${f} is missing (run make screenshots)`);
      }
    }
  }
};
walk(site);
if (bad.length) {
  console.error(bad.join('\n'));
  process.exit(1);
}
