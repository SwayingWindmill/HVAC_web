const resourceExpression = /^\$\{([A-Z0-9_]+):-([^}]+)\}$/;

export function parseRuntimeEnvironment(text) {
  const values = {};
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const separator = line.indexOf("=");
    if (separator <= 0) continue;
    values[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  }
  return values;
}

function parseProfiles(block) {
  const match = block.match(/^    profiles:\s*\[([^\]]+)\]/m);
  if (!match) return [];
  return match[1]
    .split(",")
    .map((value) => value.trim().replace(/^['"]|['"]$/g, ""));
}

function parseResource(block, field) {
  const match = block.match(
    new RegExp(`^    ${field}:\\s*(\\$\\{[A-Z0-9_]+:-[^}]+\\})`, "m"),
  );
  if (!match) return null;
  const expression = match[1].match(resourceExpression);
  if (!expression)
    throw new Error(`Unsupported Compose ${field} expression: ${match[1]}`);
  return { variable: expression[1], defaultValue: expression[2] };
}

export function parseComposeResourceModel(compose) {
  const servicesMarker = "\nservices:\n";
  const start = compose.indexOf(servicesMarker);
  if (start < 0) throw new Error("Phase 1 Compose services section is missing");
  const remainder = compose.slice(start + servicesMarker.length);
  const networksStart = remainder.search(/^networks:\r?$/m);
  const servicesText =
    networksStart < 0 ? remainder : remainder.slice(0, networksStart);
  const servicePattern =
    /^  ([a-z0-9][a-z0-9-]*):\r?\n([\s\S]*?)(?=^  [a-z0-9][a-z0-9-]*:\r?$|(?![\s\S]))/gm;
  const services = [];
  for (const match of servicesText.matchAll(servicePattern)) {
    services.push({
      name: match[1],
      profiles: parseProfiles(match[2]),
      cpu: parseResource(match[2], "cpus"),
      memory: parseResource(match[2], "mem_limit"),
    });
  }
  if (services.length === 0)
    throw new Error("Phase 1 Compose resource model is empty");
  return services;
}

function parseCpu(value) {
  const cpu = Number(value);
  if (!Number.isFinite(cpu) || cpu <= 0)
    throw new Error(`Invalid CPU limit: ${value}`);
  return cpu;
}

function parseMemoryGiB(value) {
  const match = String(value)
    .trim()
    .match(/^(\d+(?:\.\d+)?)([kKmMgG]?)$/);
  if (!match) throw new Error(`Invalid memory limit: ${value}`);
  const amount = Number(match[1]);
  const unit = match[2].toLowerCase();
  if (unit === "g") return amount;
  if (unit === "m") return amount / 1024;
  if (unit === "k") return amount / (1024 * 1024);
  return amount / (1024 * 1024 * 1024);
}

function selectedService(service, profiles) {
  return (
    service.profiles.length === 0 ||
    service.profiles.some((profile) => profiles.includes(profile))
  );
}

function calculateResourceTotals(services, profiles, overrides) {
  let cpuLimitCores = 0;
  let memoryGiB = 0;
  for (const service of services) {
    if (!selectedService(service, profiles)) continue;
    if (service.cpu)
      cpuLimitCores += parseCpu(
        overrides[service.cpu.variable] ?? service.cpu.defaultValue,
      );
    if (service.memory)
      memoryGiB += parseMemoryGiB(
        overrides[service.memory.variable] ?? service.memory.defaultValue,
      );
  }
  return {
    cpuLimitCores: Number(cpuLimitCores.toFixed(2)),
    memoryGiB: Number(memoryGiB.toFixed(2)),
  };
}

export function resolveDeploymentTier({
  contract,
  compose,
  tierId,
  environment,
  runtimeEnvironment = {},
  additionalComposeDocuments = [],
  additionalProfiles = [],
}) {
  const tier = contract.tiers?.find((candidate) => candidate.id === tierId);
  if (!tier) throw new Error(`Unknown Phase 1 deployment tier: ${tierId}`);
  if (environment === "production" && tier.productionAllowed !== true) {
    throw new Error(
      `Phase 1 deployment tier ${tierId} is not allowed for production`,
    );
  }
  if (!Array.isArray(tier.profiles) || tier.profiles.length !== 1) {
    throw new Error(
      `Phase 1 deployment tier ${tierId} must select exactly one observability profile`,
    );
  }
  if (
    !Number.isFinite(tier.maximumCpuLimitOvercommitRatio) ||
    tier.maximumCpuLimitOvercommitRatio < 1
  ) {
    throw new Error(
      `Phase 1 deployment tier ${tierId} must declare maximumCpuLimitOvercommitRatio`,
    );
  }

  const overrides = tier.resourceOverrides ?? {};
  const runtimeOverrides = tier.runtimeOverrides ?? {};
  const services = [
    ...parseComposeResourceModel(compose),
    ...additionalComposeDocuments.flatMap(parseComposeResourceModel),
  ];
  const knownVariables = new Set(
    services
      .flatMap((service) => [service.cpu?.variable, service.memory?.variable])
      .filter(Boolean),
  );
  for (const variable of Object.keys(overrides)) {
    if (!knownVariables.has(variable))
      throw new Error(
        `Deployment tier ${tierId} overrides unknown Compose resource ${variable}`,
      );
  }
  const effectiveResourceValues = { ...runtimeEnvironment, ...overrides };
  const resourceProfiles = [...tier.profiles, ...additionalProfiles];
  const resourceTotals = calculateResourceTotals(
    services,
    resourceProfiles,
    effectiveResourceValues,
  );
  if (resourceTotals.memoryGiB > tier.resourceBudget.memoryGiB) {
    throw new Error(
      `Deployment tier ${tierId} memory limits ${resourceTotals.memoryGiB} GiB exceed host budget ${tier.resourceBudget.memoryGiB} GiB`,
    );
  }
  const cpuLimitBudget =
    tier.resourceBudget.cpuCores * tier.maximumCpuLimitOvercommitRatio;
  if (resourceTotals.cpuLimitCores > cpuLimitBudget) {
    throw new Error(
      `Deployment tier ${tierId} CPU limits ${resourceTotals.cpuLimitCores} exceed certified overcommit budget ${cpuLimitBudget}`,
    );
  }

  return {
    tier,
    profiles: [...tier.profiles],
    maximumCpuLimitOvercommitRatio: tier.maximumCpuLimitOvercommitRatio,
    resourceTotals,
    resourceProfiles,
    environment: {
      ...overrides,
      ...runtimeOverrides,
      PHASE1_DEPLOYMENT_TIER: tier.id,
      PHASE1_OBSERVABILITY_PROFILE: tier.profiles[0],
    },
  };
}
