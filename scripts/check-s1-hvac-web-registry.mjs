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
  realSystem,
  registryAdministration,
  registryResources,
  registryTemplates,
  registryImportExport,
] = await Promise.all([
  read('apps/hvac-web/src/api/generated/platformGateway.gen.ts'),
  read('apps/hvac-web/src/api/registry.ts'),
  read('apps/hvac-web/src/real/assets/RealAssetsWorkspace.tsx'),
  read('apps/hvac-web/src/real/RealSystemManagement.tsx'),
  read('apps/hvac-web/src/real/registry-admin/RegistryAdministration.tsx'),
  read('apps/hvac-web/src/real/registry-admin/RegistryResourceWorkbench.tsx'),
  read('apps/hvac-web/src/real/registry-admin/RegistryTemplateWorkbench.tsx'),
  read('apps/hvac-web/src/real/registry-admin/RegistryImportExportWorkbench.tsx'),
]);

for (const method of [
  'listSites',
  'getSite',
  'listSiteAssets',
  'getAsset',
  'listSiteDevices',
  'listSiteDeviceBindings',
  'getSiteAssetModel',
  'getDevice',
  'listSiteSpaceChildren',
  'listDevicePoints',
  'createSite',
  'updateSite',
  'createSiteSpace',
  'updateSiteSpace',
  'createSiteAsset',
  'updateSiteAsset',
  'createSiteDevice',
  'updateSiteDevice',
  'createSitePoint',
  'updateSitePoint',
  'rebindRegistryResource',
  'releaseRegistryTemplateRevision',
  'assignRegistryTemplateRevision',
  'planRegistryImport',
  'commitRegistryImport',
  'beginRegistryRetirement',
]) {
  assert(generated.includes(`${method}:`), `generated Registry client is missing ${method}`);
}

for (const hook of [
  'useRegistrySites',
  'useRegistrySite',
  'useRegistryAssetModel',
  'useRegistryAssets',
  'useRegistryAssetDetail',
  'useRegistryDevices',
  'useRegistryDeviceDetail',
  'useRegistryDeviceBindings',
  'useAuthorizedRegistrySites',
  'useRegistrySpaceChildren',
  'useRegistryDevicePoints',
]) {
  assert(registryApi.includes(`function ${hook}`), `Registry adapter is missing ${hook}`);
}

for (const forbidden of [
  'listOrganizations',
  'getOrganization',
  'listOrganizationSites',
  'useRegistryOrganizations',
  'useRegistryEquipment',
  'listSiteEquipment',
  'getEquipment',
]) {
  assert(!generated.includes(`${forbidden}:`), `generated Registry client still exposes obsolete ${forbidden}`);
  assert(!registryApi.includes(`function ${forbidden}`), `Registry adapter still exposes obsolete ${forbidden}`);
}

for (const forbidden of ['@/mock/', 'mockSites', 'mockAssetTree', 'localStorage', 'sessionStorage']) {
  assert(!realAssets.includes(forbidden), `Real Assets workspace references ${forbidden}`);
  assert(!realSystem.includes(forbidden), `Real System Management references ${forbidden}`);
  assert(!registryAdministration.includes(forbidden), `Registry administration references ${forbidden}`);
  assert(!registryResources.includes(forbidden), `Registry resource workbench references ${forbidden}`);
  assert(!registryTemplates.includes(forbidden), `Registry template workbench references ${forbidden}`);
  assert(!registryImportExport.includes(forbidden), `Registry import/export workbench references ${forbidden}`);
}

assert(realSystem.includes("label: 'Registry 管理'"), 'Real System Management does not expose the Registry administration tab');
assert(realSystem.includes('<RegistryAdministration'), 'Real System Management does not mount RegistryAdministration');

for (const marker of [
  'registerUnsavedDraft',
  'destroyOnHidden',
  'useRegistrySites',
  'useRegistryAssetModel',
  "capabilities.has('site.write')",
]) {
  assert(registryAdministration.includes(marker), `Registry administration is missing ${marker}`);
}

for (const marker of [
  'getRegistrySpaceChildren',
  'useRegistryDevicePoints',
  'registryAdminApi.rebind',
  'registryAdminApi.retire',
  'Expected Revision',
  'counterDecreaseMode',
  'counterRolloverModulus',
  'sourceMetadata: entity ?',
]) {
  assert(registryResources.includes(marker), `Registry resource workbench is missing ${marker}`);
}

for (const marker of [
  'registryAdminApi.releaseTemplate',
  'registryAdminApi.assignTemplate',
  'TemplateRevision 发布后不可修改',
  '再次 Assign',
]) {
  assert(registryTemplates.includes(marker), `Registry template workbench is missing ${marker}`);
}

for (const marker of [
  'registryAdminApi.planImport',
  'registryAdminApi.commitImport',
  'canCommitImportPlan',
  'buildRegistryExport',
  'sourceMetadata',
]) {
  assert(registryImportExport.includes(marker), `Registry import/export workbench is missing ${marker}`);
}

for (const forbidden of ['X-Site-Id', 'localStorage', 'sessionStorage']) {
  assert(!registryApi.includes(forbidden), `Registry adapter uses forbidden navigation/authorization source ${forbidden}`);
}
assert(registryApi.includes('MAX_NAVIGATION_PAGES'), 'authorized Site navigation collection is not bounded');
assert(registryApi.includes('PlatformApiError'), 'Registry adapter does not preserve typed Problem Details');
assert(registryApi.includes('uuidV7Schema.parse'), 'Registry adapter does not validate platform UUIDv7 path inputs');
assert(registryApi.includes('ZodError'), 'Registry adapter does not expose typed invalid-link state');
assert(registryApi.includes("API_MODE === 'real'"), 'Registry requests are not explicitly gated to Real mode');
assert(registryApi.includes('REGISTRY_REVISION_CONFLICT'), 'Registry adapter does not expose stale Revision conflicts');
assert(registryApi.includes('REGISTRY_IMPORT_PLAN_INVALID'), 'Registry adapter does not expose invalidated import plans');

console.log('S1 HVAC Web Registry static gate passed: canonical generated clients, Real Registry administration, conflict safety, dirty-draft protection and no mock fallback verified.');
