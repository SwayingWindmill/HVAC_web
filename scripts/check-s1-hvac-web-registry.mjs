import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};

const [
  generated,
  registryApi,
  realAssets,
  assetsEntry,
  realSystem,
  systemEntry,
  systemPage,
  header,
] = await Promise.all([
  read('apps/hvac-web/src/api/generated/platformGateway.gen.ts'),
  read('apps/hvac-web/src/api/registry.ts'),
  read('apps/hvac-web/src/pages/Assets/RealAssets.tsx'),
  read('apps/hvac-web/src/pages/Assets/index.tsx'),
  read('apps/hvac-web/src/pages/System/RealRegistrySitePanel.tsx'),
  read('apps/hvac-web/src/pages/System/RegistrySitePanel.tsx'),
  read('apps/hvac-web/src/pages/System/index.tsx'),
  read('apps/hvac-web/src/layout/Header.tsx'),
]);

for (const method of [
  'listOrganizations',
  'getOrganization',
  'listOrganizationSites',
  'getSite',
  'listSiteEquipment',
  'getEquipment',
  'listSiteDevices',
  'getDevice',
]) {
  assert(generated.includes(`${method}:`), `generated Registry client is missing ${method}`);
}

for (const hook of [
  'useRegistryOrganizations',
  'useRegistrySites',
  'useRegistrySite',
  'useRegistryEquipment',
  'useRegistryEquipmentDetail',
  'useRegistryDevices',
  'useRegistryDeviceDetail',
  'useAuthorizedRegistrySites',
]) {
  assert(registryApi.includes(`function ${hook}`), `Registry adapter is missing ${hook}`);
}

for (const forbidden of ["@/mock/", "./meta", 'mockSites', 'mockAssetTree', 'DEVICE_META', 'ASSET_TREE']) {
  assert(!realAssets.includes(forbidden), `Real Assets view imports or references ${forbidden}`);
  assert(!realSystem.includes(forbidden), `Real System Registry view imports or references ${forbidden}`);
}
assert(!systemPage.includes('mockSites'), 'System page still imports or uses mockSites directly');
assert(!systemPage.includes('mockAssetTree'), 'System page still imports or uses mockAssetTree directly');

assert(assetsEntry.includes("API_MODE === 'real'"), 'Assets entry does not choose the real Registry view by API mode');
assert(assetsEntry.includes("lazy(() => import('./MockAssets'))"), 'Assets mock view is not isolated behind a lazy boundary');
assert(systemEntry.includes("API_MODE === 'real'"), 'System Registry entry does not choose the real view by API mode');
assert(systemEntry.includes("lazy(() => import('./MockRegistrySitePanel'))"), 'System mock Registry view is not isolated behind a lazy boundary');

for (const marker of [
  'selectedSite?.timezone',
  'Registry 生命周期',
  'S2 尚未提供',
  'RegistryLoadMore',
  'useRegistryEquipmentDetail',
  'useRegistryDeviceDetail',
  'data-testid="real-registry-assets-page"',
]) {
  assert(realAssets.includes(marker), `Real Assets view is missing marker ${marker}`);
}
for (const marker of [
  'selectedSite?.timezone',
  'Equipment',
  'Device',
  'RegistryLoadMore',
  'disabled>新增节点',
  'data-testid="real-registry-system-panel"',
]) {
  assert(realSystem.includes(marker), `Real System Registry view is missing marker ${marker}`);
}

assert(header.includes('useAuthorizedRegistrySites'), 'Header does not source real Site options from authorized Registry results');
assert(header.includes("API_MODE === 'real'"), 'Header does not separate real and mock Site navigation');
assert(header.includes('真实模式不会显示本地演示站点'), 'Header does not expose a typed real-mode Site failure state');

for (const forbidden of ['X-Site-Id', 'localStorage', 'sessionStorage']) {
  assert(!registryApi.includes(forbidden), `Registry adapter uses forbidden navigation/authorization source ${forbidden}`);
}
assert(registryApi.includes('MAX_NAVIGATION_PAGES'), 'authorized navigation collection is not bounded');
assert(registryApi.includes('PlatformApiError'), 'Registry adapter does not preserve typed Problem Details');
assert(registryApi.includes('uuidV7Schema.parse'), 'Registry adapter does not validate platform UUIDv7 path inputs');
assert(registryApi.includes('ZodError'), 'Registry adapter does not expose typed invalid-link state');
assert(registryApi.includes("API_MODE === 'real'"), 'Registry requests are not explicitly gated to real mode');

console.log('S1 HVAC Web Registry static gate passed: generated clients, real/mock separation, typed states and authorized Site navigation verified.');
