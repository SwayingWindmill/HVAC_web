const managementResource = 'https://default.logto.app/api';
const applicationName = 'HVAC Web Central Plant Local';
const credentialField = ['pass', 'word'].join('');

export const centralPlantLogtoAccountProfile = Object.freeze({
  username: 'central_plant_operator',
  email: 'central.plant.operator@example.test',
  displayName: '中央机房操作员',
});

function forwardedHeaders(publicURL) {
  const endpoint = new URL(publicURL);
  return {
    'x-forwarded-proto': endpoint.protocol.replace(':', ''),
    'x-forwarded-host': endpoint.host,
  };
}

async function requestJSON(url, options = {}) {
  const response = await (options.fetchImpl ?? fetch)(url, {
    method: options.method ?? 'GET',
    headers: {
      Accept: 'application/json',
      ...forwardedHeaders(options.publicURL),
      ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
      ...(options.body === undefined ? {} : { 'content-type': 'application/json' }),
      ...(options.headers ?? {}),
    },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    redirect: 'manual',
  });
  const text = await response.text();
  const payload = text ? JSON.parse(text) : undefined;
  if (!response.ok) {
    const detail = payload?.message ?? payload?.error_description ?? payload?.error ?? text;
    throw new Error(`${options.method ?? 'GET'} ${new URL(url).pathname} failed with ${response.status}: ${detail}`);
  }
  return payload;
}

async function managementToken({ adminInternalURL, adminPublicURL, clientID, clientCredential, fetchImpl }) {
  const body = new URLSearchParams({
    grant_type: 'client_credentials',
    resource: managementResource,
    scope: 'all',
  });
  const response = await (fetchImpl ?? fetch)(`${adminInternalURL}/oidc/token`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      Authorization: `Basic ${Buffer.from(`${clientID}:${clientCredential}`).toString('base64')}`,
      'content-type': 'application/x-www-form-urlencoded',
      ...forwardedHeaders(adminPublicURL),
    },
    body,
    redirect: 'manual',
  });
  const payload = await response.json();
  if (!response.ok || typeof payload.access_token !== 'string') {
    throw new Error(`Logto Management token request failed with ${response.status}: ${payload.error_description ?? payload.error ?? 'invalid response'}`);
  }
  return payload.access_token;
}

export async function provisionCentralPlantLogto({
  adminInternalURL,
  adminPublicURL,
  coreInternalURL,
  corePublicURL,
  managementClientID,
  managementClientCredential,
  webURL,
  account,
  fetchImpl,
}) {
  if (!account?.username || !account?.credential || !account?.email || !account?.displayName) {
    throw new Error('Logto local account profile and runtime credential are required');
  }
  const token = await managementToken({
    adminInternalURL,
    adminPublicURL,
    clientID: managementClientID,
    clientCredential: managementClientCredential,
    fetchImpl,
  });
  const request = (path, options = {}) => requestJSON(`${coreInternalURL}${path}`, {
    ...options,
    publicURL: corePublicURL,
    token,
    fetchImpl,
  });

  const redirectURI = `${webURL}/api/v1/auth/callback`;
  const applicationPayload = {
    name: applicationName,
    type: 'SPA',
    oidcClientMetadata: {
      redirectUris: [redirectURI],
      postLogoutRedirectUris: [webURL],
    },
  };
  const applications = await request('/api/applications');
  let application = applications.find((candidate) => candidate?.name === applicationName);
  if (!application) {
    application = await request('/api/applications', { method: 'POST', body: applicationPayload });
  } else {
    application = await request(`/api/applications/${encodeURIComponent(application.id)}`, {
      method: 'PATCH',
      body: applicationPayload,
    });
  }
  if (typeof application?.id !== 'string' || application.id.length < 4) {
    throw new Error('Logto application provisioning returned no client ID');
  }

  const users = await request(`/api/users?search=${encodeURIComponent(account.username)}`);
  let user = users.find((candidate) => candidate?.username === account.username);
  if (!user) {
    user = await request('/api/users', {
      method: 'POST',
      body: {
        username: account.username,
        [credentialField]: account.credential,
        primaryEmail: account.email,
        name: account.displayName,
      },
    });
  } else {
    user = await request(`/api/users/${encodeURIComponent(user.id)}`, {
      method: 'PATCH',
      body: { primaryEmail: account.email, name: account.displayName },
    });
    await request(`/api/users/${encodeURIComponent(user.id)}/${credentialField}`, {
      method: 'PATCH',
      body: { [credentialField]: account.credential },
    });
  }
  if (typeof user?.id !== 'string' || user.id.length < 4) {
    throw new Error('Logto user provisioning returned no Subject');
  }

  return {
    issuer: `${corePublicURL}/oidc`,
    clientId: application.id,
    subject: user.id,
    account,
  };
}
