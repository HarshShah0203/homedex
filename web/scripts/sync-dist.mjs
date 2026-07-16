import { cp, mkdir, readdir, rm } from 'node:fs/promises';
import { resolve } from 'node:path';

const dist = resolve('dist');
const target = resolve('../internal/server/static');

await rm(target, { recursive: true, force: true });
await mkdir(target, { recursive: true });
for (const entry of await readdir(dist)) await cp(resolve(dist, entry), resolve(target, entry), { recursive: true });
