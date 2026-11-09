// Real Chromium regression checks using only Node's built-in modules.
// Usage: node scripts/check-engagement-map.mjs [/absolute/screenshot.png]
import { createServer } from 'node:http';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../web/static/', import.meta.url)).replace(/\/$/, '');
const cat = 'Music';
const base = { language: 'en', game_name: cat, started_at: '2026-11-01T00:00:00Z', scored_at: '2026-11-02T00:00:00Z',
  quotient: 30, confidence: 0.8, final: true, expected_per_k: 10, z: -4, score_version: 10,
  eligible_minutes: 60, silent_minutes: 40, eligible_messages: 200 };
const points = [
  { ...base, stream_id: 'zero', display_name: 'Intermittent conversation', median_viewers: 500, chatters_per_k: 0, aggregate_per_k: 3.333 },
  { ...base, stream_id: 'tiny', display_name: 'Small positive rate', median_viewers: 1000, chatters_per_k: 0.001, aggregate_per_k: 0.002 },
  { ...base, stream_id: 'ordinary', display_name: 'Ordinary activity', median_viewers: 2000, chatters_per_k: 10, aggregate_per_k: 12 },
  { ...base, stream_id: 'legacy', display_name: 'Older score', language: 'ja', median_viewers: 4000, chatters_per_k: 2,
    aggregate_per_k: null, eligible_minutes: null, silent_minutes: null, eligible_messages: null, score_version: 9 },
];
const fit = (a, n) => ({ a, slope: -0.3, sd: 0.5, n });
const data = { days: 7, limit: 3000, truncated: true, points,
  fits: { '': fit(4, 20), 'cat:Music': fit(3, 18), 'selection:["en","Music"]': fit(2, 15) },
  aggregate_fits: { '': fit(4, 20), 'selection:["en","Music"]': fit(3, 16) },
  sample: { oldest_scored_at: base.started_at, newest_scored_at: base.scored_at, channels: 4, summaries: 3 } };

async function clientChecks() {
  const { renderEngagementMap } = await import('/viz.js');
  const assert = (ok, message) => { if (!ok) throw Error(message); };
  const change = (id, value) => { const e = document.getElementById(id); e.value = value; e.dispatchEvent(new Event('change')); };
  for (let i = 0; !document.querySelector('[data-stream-id="zero"]') && i < 100; i++) await new Promise((r) => setTimeout(r, 20));
  const chart = document.getElementById('em');
  const cy = (id) => +chart.querySelector(`[data-stream-id="${id}"]`).getAttribute('cy');
  assert(cy('zero') > cy('tiny') && cy('tiny') > cy('ordinary'), 'zero and small positive values must remain distinct');
  const mark = chart.querySelector('[data-stream-id="zero"]').getBoundingClientRect();
  chart.querySelector('.hit').dispatchEvent(new PointerEvent('pointermove', { clientX: mark.x + mark.width / 2, clientY: mark.y + mark.height / 2 }));
  const tip = document.getElementById('tooltip');
  assert(!tip.hidden && tip.textContent.includes('Silent eligible minutes') && tip.textContent.includes('200'), 'activity summary missing from tooltip');
  chart.querySelector('.hit').dispatchEvent(new PointerEvent('pointerleave'));
  assert(document.getElementById('em-sample').textContent.includes('cap was reached'), 'sample cap missing');
  assert(document.getElementById('em-sample').textContent.includes('streams may have started earlier'), 'score-window wording missing');
  change('em-lang', 'en'); change('em-cat', 'Music');
  assert(chart.textContent.includes('Reference: 15 retained streams'), 'combined selection used another population');
  assert(chart.querySelectorAll('[data-stream-id]').length === 3, 'combined selection did not filter dots');
  change('em-metric', 'aggregate');
  assert(chart.textContent.includes('Reference: 16 retained streams'), 'aggregate line used median fit');
  assert(chart.querySelector('[data-stream-id="zero"]').getAttribute('data-rate') === '3.333', 'intermittent chat remained zero in aggregate view');
  change('em-lang', ''); change('em-cat', '');
  assert(chart.querySelectorAll('[data-stream-id]').length === 3 && chart.textContent.includes('omitted'), 'legacy summary was invented');
  change('em-metric', 'median'); change('em-lang', 'ja');
  assert(!chart.querySelector('.fit-line'), 'small selection silently borrowed a line');
  assert(location.search.includes('lang=ja'), 'language selection is not retained in URL');
  change('em-days', '1');
  for (let i = 0; !document.querySelector('[data-stream-id="legacy"]') && i < 100; i++) await new Promise((r) => setTimeout(r, 20));
  assert(document.getElementById('em-lang').value === 'ja' && location.search.includes('days=1'), 'window change lost selected language');
  const currentChart = document.getElementById('em');
  const fixture = await (await fetch('/api/engagement')).json();
  renderEngagementMap(currentChart, { ...fixture, points: [{ ...fixture.points[0], aggregate_per_k: 0 }], fits: {} }, '');
  assert(!currentChart.querySelector('svg').outerHTML.includes('NaN'), 'all-zero selection produced invalid geometry');
  renderEngagementMap(currentChart, { ...fixture, points: [], fits: {} }, '');
  assert(currentChart.textContent.includes('No measurements'), 'empty selection missing');
  // Restore the complete page for visual inspection.
  change('em-lang', '');
  assert(document.documentElement.scrollWidth <= innerWidth, 'page overflows viewport');
  const result = document.createElement('pre'); result.id = 'map-check-result'; result.textContent = 'PASS'; document.body.append(result);
}

const server = createServer(async (req, res) => {
  try {
    const path = new URL(req.url, 'http://localhost').pathname;
    if (path === '/api/engagement') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(data)); return; }
    if (path === '/engagement') {
      res.setHeader('Content-Type', 'text/html');
      const html = (await readFile(join(root, 'index.html'), 'utf8')).replace(/{{[\s\S]*?}}/g, '')
        .replace('<html lang="en">', `<html lang="en" data-theme="${process.env.THEME === 'light' ? 'light' : 'dark'}">`)
        .replace('</body>', `<script type="module">(${clientChecks.toString()})().catch(e=>{const p=document.createElement('pre');p.id='map-check-result';p.textContent='FAIL: '+e.message;document.body.append(p);});</script></body>`);
      res.end(html); return;
    }
    const file = resolve(root, '.' + path);
    if (!file.startsWith(root + '/')) { res.writeHead(404).end(); return; }
    const type = path.endsWith('.js') ? 'text/javascript' : path.endsWith('.css') ? 'text/css' : 'application/octet-stream';
    res.setHeader('Content-Type', type); res.end(await readFile(file));
  } catch { res.writeHead(404).end(); }
});
const profile = await mkdtemp(join(tmpdir(), 'headcount-map-'));
try {
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  const args = ['--headless', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-proxy-server',
    '--force-prefers-reduced-motion', `--user-data-dir=${profile}`, `--window-size=${process.env.WIDTH || 1280},1600`,
    '--virtual-time-budget=5000', '--dump-dom'];
  if (process.argv[2]) args.push(`--screenshot=${resolve(process.argv[2])}`);
  args.push(`http://127.0.0.1:${server.address().port}/engagement`);
  const child = spawn(process.env.CHROMIUM || '/usr/bin/chromium', args, { stdio: ['ignore', 'pipe', 'pipe'] });
  let output = '', errors = '';
  child.stdout.on('data', (d) => { output += d; }); child.stderr.on('data', (d) => { errors += d; });
  const timer = setTimeout(() => child.kill('SIGKILL'), 30000);
  const code = await new Promise((done, reject) => { child.on('error', reject); child.on('close', done); });
  clearTimeout(timer);
  const result = output.match(/<pre id="map-check-result">([^<]+)<\/pre>/)?.[1];
  if (code !== 0 || result !== 'PASS') throw Error(result || `Chromium exited ${code}: ${errors.slice(-1000)}`);
  console.log(`PASS: engagement map, ${process.env.WIDTH || 1280}px, ${process.env.THEME || 'dark'} theme`);
} finally {
  await new Promise((done) => server.close(done));
  await rm(profile, { recursive: true, force: true });
}
