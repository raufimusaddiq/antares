const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright-core');
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE, args: ['--no-sandbox'] });
const output = fileURLToPath(new URL('.', import.meta.url));
await mkdir(output, { recursive: true });
const page = await browser.newPage({ viewport: { width: 390, height: 844 }, colorScheme: 'dark', reducedMotion: 'reduce' });
const errors = [];
page.on('pageerror', e => errors.push(e.message));
await page.addInitScript(() => {
  window.EventSource = class extends EventTarget {
    static CLOSED = 2;
    readyState = 1;
    constructor() { super(); window.testStream = this; setTimeout(() => this.onopen?.(), 0); }
    close() { this.readyState = 2; }
  };
});
const id = '11111111111111111111111111111111';
const chat = { id, title: 'Scroll and keyboard verification fixture', activeRunId: 'fixture-run' };
const messages = Array.from({ length: 28 }, (_, seq) => ({ seq, role: seq % 2 ? 'assistant' : 'user', text: seq % 2 ? 'This is a local test fixture for transcript scrolling.\n\n' + 'A long line should wrap within the available conversation width. '.repeat(4) : 'Check streaming, keyboard access, and responsive layout.' }));
await page.route('**/api/**', route => {
  const path = new URL(route.request().url()).pathname;
  let body;
  if (path === '/api/me') body = { email: 'layout-fixture@example.test', state: 'ACTIVE', canAdmin: true };
  else if (path === '/api/chats') body = { chats: [chat], nextCursor: '' };
  else if (path.endsWith('/messages')) body = { messages, nextCursor: '', total: messages.length };
  else if (path.startsWith('/api/runs/')) body = { runId: 'fixture-run', status: 'RUNNING' };
  else if (path === '/api/notifications') body = { configured: false, devices: [], mode: 'off' };
  else return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND' } } });
  return route.fulfill({ json: body });
});
await page.goto('http://127.0.0.1:5185');
await page.getByRole('feed').waitFor();
await page.getByText('Working…', { exact: true }).waitFor();
await page.evaluate(() => document.fonts.ready);
const geometry = () => page.evaluate(() => {
  const feed = document.querySelector('.log');
  const composer = document.querySelector('.composer').getBoundingClientRect();
  return { width: innerWidth, overflow: document.documentElement.scrollWidth, top: feed.scrollTop, bottom: feed.scrollHeight - feed.clientHeight, composer: { x: composer.x, y: composer.y, right: composer.right, bottom: composer.bottom }, height: innerHeight };
});
console.log('Initial phone', await geometry());
const delta = async text => {
  await page.evaluate(text => window.testStream.dispatchEvent(new MessageEvent('assistant.delta', { data: JSON.stringify({ text }) })), text);
  await page.waitForTimeout(150);
};
for (const [name, width, height] of [['phone', 390, 844], ['desktop', 1440, 1000]]) {
  await page.setViewportSize({ width, height });
  await page.waitForTimeout(200);
  const feed = page.getByRole('feed');
  await feed.evaluate(el => { el.scrollTop = el.scrollHeight; });
  await page.waitForTimeout(150);
  await page.waitForFunction(() => { const el = document.querySelector('.log'); return Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop) < 2; });
  console.log(name, 'before stream', await geometry(), await page.getByRole('button', { name: 'Jump to latest' }).count());
  await delta('Streaming verification fixture.\n\n' + 'Content arrives while the reader stays at the bottom. '.repeat(18));
  await page.waitForFunction(() => { const el = document.querySelector('.log'); return Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop) < 2; });
  let g = await geometry();
  console.log(name, 'after stream', g);
  assert(Math.abs(g.top - g.bottom) < 2, `${name}: follows stream`);
  await feed.evaluate(el => { el.scrollTop -= 550; });
  await page.waitForTimeout(100);
  const before = await geometry();
  await delta('\n\nStreaming continues while the reader reviews earlier messages. '.repeat(12));
  g = await geometry();
  assert(Math.abs(g.top - before.top) < 2, `${name}: holds scroll position`);
  await page.getByRole('button', { name: 'Jump to latest' }).click();
  await page.waitForTimeout(100);
  g = await geometry();
  assert(Math.abs(g.top - g.bottom) < 2, `${name}: jumps to latest`);
  await page.getByRole('button', { name: 'New chat', exact: true }).focus();
  await page.keyboard.press('Alt+Shift+M');
  assert(await page.locator('#composer-input').evaluate(el => el === document.activeElement));
  await page.locator('#composer-input').fill('');
  await page.keyboard.type('Local fixture draft / preserved while typing');
  const search = page.getByRole('searchbox');
  await search.focus();
  await page.keyboard.press('Alt+Shift+M');
  await page.keyboard.type('fixture/');
  assert.equal(await search.inputValue(), 'fixture/');
  assert(await search.evaluate(el => el === document.activeElement));
  await search.fill('');
  await page.locator('#composer-input').focus();
  g = await geometry();
  assert(g.overflow <= width, `${name}: no horizontal overflow`);
  assert(g.composer.x >= 0 && g.composer.right <= width && g.composer.bottom <= height, `${name}: composer fits`);
  await feed.evaluate(el => { el.scrollTop = 300; });
  await page.getByRole('button', { name: 'Jump to latest' }).waitFor();
  await page.waitForTimeout(100);
  await page.screenshot({ path: `${output}/${name}.png` });
  console.log(name, g);
}
for (const width of [320, 640, 768, 1024, 1920]) {
  await page.setViewportSize({ width, height: 800 });
  await page.waitForTimeout(200);
  const g = await geometry();
  assert(g.overflow <= width, `${width}: no horizontal overflow`);
  assert(g.composer.x >= 0 && g.composer.right <= width && g.composer.bottom <= 800, `${width}: composer fits`);
}
await page.setViewportSize({ width: 390, height: 500 });
await page.emulateMedia({ colorScheme: 'light' });
await page.getByRole('button', { name: 'Notifications', exact: true }).click();
const panel = await page.getByRole('region', { name: 'Notifications' }).boundingBox();
assert(panel.x >= 0 && panel.x + panel.width <= 390, 'phone notification panel fits');
await page.getByRole('button', { name: 'Notifications', exact: true }).click();
const short = await geometry();
assert(short.composer.bottom <= 500, 'composer fits shortened viewport');
await page.getByRole('button', { name: 'Jump to latest' }).click();
await page.getByRole('feed').focus();
await page.keyboard.press('Tab');
assert(await page.getByRole('button', { name: 'Cancel', exact: true }).evaluate(el => el === document.activeElement), 'Tab reaches live-run control');
await page.keyboard.press('Tab');
assert(await page.locator('#composer-input').evaluate(el => el === document.activeElement), 'Tab reaches composer from transcript end');
await page.screenshot({ path: `${output}/phone-light-short.png` });
assert.deepEqual(errors, []);
console.log('PASS: streaming hold/follow, jump, shortcut, typing, fit; browser', browser.version());
await browser.close();
