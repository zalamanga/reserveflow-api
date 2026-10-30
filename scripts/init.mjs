import { randomBytes, createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const file = resolve(root, '.env');
mkdirSync(resolve(root, '.local'), { recursive: true });
if (!existsSync(file)) {
  const values = {};
  for (const key of ['DB_PASSWORD', 'ALPHA_API_KEY', 'BETA_API_KEY']) {
    const raw = process.env[key] || randomBytes(32).toString('hex');
    if (!/^[a-f0-9]{64}$/.test(raw)) throw new Error(`${key} must be 64 lowercase hexadecimal characters`);
    values[key] = raw;
  }
  if (values.ALPHA_API_KEY === values.BETA_API_KEY) throw new Error('Tenant keys must differ');
  for (const team of ['ALPHA', 'BETA']) values[`${team}_KEY_SHA256`] = createHash('sha256').update(values[`${team}_API_KEY`]).digest('hex');
  writeFileSync(file, Object.entries(values).map(([key, value]) => `${key}=${value}`).join('\n') + '\n', { flag: 'wx', mode: 0o600 });
}
const text = readFileSync(file, 'utf8');
const values = Object.fromEntries(text.split(/\r?\n/).filter(line => line && !line.startsWith('#')).map(line => { const n = line.indexOf('='); return [line.slice(0,n),line.slice(n+1)]; }));
for (const key of ['DB_PASSWORD','ALPHA_API_KEY','BETA_API_KEY','ALPHA_KEY_SHA256','BETA_KEY_SHA256']) if (!/^[a-f0-9]{64}$/.test(values[key] || '')) throw new Error(`Invalid ${key} in .env`);
for (const team of ['ALPHA','BETA']) if (createHash('sha256').update(values[`${team}_API_KEY`]).digest('hex') !== values[`${team}_KEY_SHA256`]) throw new Error(`Mismatched ${team} key digest`);
if (values.ALPHA_API_KEY === values.BETA_API_KEY) throw new Error('Tenant keys must differ');
writeFileSync(resolve(root,'.local','LOCAL-CREDENTIALS.txt'), `ReserveFlow local demo only\nBase URL: http://127.0.0.1:8099\nAlpha Bearer key: ${values.ALPHA_API_KEY}\nBeta Bearer key: ${values.BETA_API_KEY}\n\nDo not commit, share screenshots, or paste these values in public issues.\n`, { mode: 0o600 });
console.log('Local credentials ready: .env and .local/LOCAL-CREDENTIALS.txt');
