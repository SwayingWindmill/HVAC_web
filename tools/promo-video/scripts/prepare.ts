// Copies what the film page loads from node_modules into film/assets: GSAP, and the two font
// families (Noto Sans SC for Chinese, Geist for figures and Latin, the product's own face).
import { cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const film = join(here, '..', 'film', 'assets');
const require = createRequire(import.meta.url);
const packageDir = (name) => dirname(require.resolve(`${name}/package.json`, { paths: [join(here, '..'), join(here, '..', '..', '..')] }));

mkdirSync(join(film, 'vendor'), { recursive: true });
cpSync(join(packageDir('gsap'), 'dist', 'gsap.min.js'), join(film, 'vendor', 'gsap.min.js'));

const noto = packageDir('@fontsource-variable/noto-sans-sc');
cpSync(join(noto, 'files'), join(film, 'fonts', 'noto-sans-sc'), { recursive: true });
const notoCss = readFileSync(join(noto, 'index.css'), 'utf8')
  .replaceAll('./files/', 'noto-sans-sc/')
  .replaceAll('font-display: swap', 'font-display: block');

const geist = packageDir('@fontsource-variable/geist');
mkdirSync(join(film, 'fonts', 'geist'), { recursive: true });
cpSync(join(geist, 'files', 'geist-latin-wght-normal.woff2'), join(film, 'fonts', 'geist', 'geist-latin-wght-normal.woff2'));
const geistCss = `@font-face {
  font-family: 'Geist Variable';
  font-style: normal;
  font-display: block;
  font-weight: 100 900;
  src: url(geist/geist-latin-wght-normal.woff2) format('woff2-variations');
}
`;

writeFileSync(join(film, 'fonts', 'fonts.css'), `${geistCss}\n${notoCss}`);
console.log('prepared film/assets/vendor and film/assets/fonts');
