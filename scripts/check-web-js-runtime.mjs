import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const installMode = process.argv.includes('--install');
const failures = [];

function resolveOptionalPackage(name) {
  try {
    return require.resolve(`${name}/package.json`);
  } catch {
    return null;
  }
}

if (process.platform !== 'linux') {
  failures.push(
    'Frontend JavaScript tooling is Linux-only. Open this repository from WSL and run npm there; do not install or build with Windows Node/npm.',
  );
}

let kernel = '';
if (process.platform === 'linux' && existsSync('/proc/version')) {
  kernel = readFileSync('/proc/version', 'utf8').trim();
}
const isWsl = /microsoft/i.test(kernel) || Boolean(process.env.WSL_DISTRO_NAME);

if (process.platform === 'linux') {
  try {
    await import('rollup');
  } catch (error) {
    failures.push(
      `Rollup could not load its Linux native dependency. Reinstall node_modules from Linux/WSL only. ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  const windowsRollupPackages = [
    '@rollup/rollup-win32-x64-msvc',
    '@rollup/rollup-win32-ia32-msvc',
    '@rollup/rollup-win32-arm64-msvc',
  ].filter((name) => resolveOptionalPackage(name));

  if (windowsRollupPackages.length > 0) {
    failures.push(
      `Windows Rollup native packages are present in this Linux node_modules (${windowsRollupPackages.join(', ')}). Remove node_modules and reinstall with WSL npm only.`,
    );
  }
}

const report = {
  ok: failures.length === 0,
  mode: installMode ? 'install' : 'runtime',
  platform: process.platform,
  arch: process.arch,
  node: process.version,
  execPath: process.execPath,
  wsl: isWsl,
  cwd: process.cwd(),
  linuxRollup: process.platform === 'linux'
    ? resolveOptionalPackage('@rollup/rollup-linux-x64-gnu')
      ?? resolveOptionalPackage('@rollup/rollup-linux-x64-musl')
      ?? resolveOptionalPackage('@rollup/rollup-linux-arm64-gnu')
      ?? resolveOptionalPackage('@rollup/rollup-linux-arm64-musl')
    : null,
};

if (failures.length > 0) {
  console.error(JSON.stringify({ ...report, failures }, null, 2));
  process.exit(1);
}

console.log(JSON.stringify(report, null, 2));
