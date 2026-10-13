// Copia in www/ i file condivisi: il lettore di QR (jsQR) e le icone del marchio.
import { cpSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
cpSync(join(root, 'node_modules/jsqr/dist/jsQR.js'), join(root, 'www/jsQR.js'));
mkdirSync(join(root, 'www/brand'), { recursive: true });
for (const f of ['icon-192.png', 'icon-512.png']) cpSync(join(root, '../web/brand', f), join(root, 'www/brand', f));
console.log('www pronta');
