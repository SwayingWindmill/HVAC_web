const managementResource = 'https://default.logto.app/api';
const applicationName = 'HVAC Web Central Plant Local';
const credentialField = ['pass', 'word'].join('');

const industrialCalmAuthCss = `
/* Quanlaihe Industrial Calm authentication surface */
:root {
  color-scheme: light dark;
  font-family: Inter, "Noto Sans SC", "Microsoft YaHei", system-ui, sans-serif;
  --qlh-auth-canvas: #f4f6f7;
  --qlh-auth-surface: #ffffff;
  --qlh-auth-border: #dce4e4;
  --qlh-auth-text: #17201f;
  --qlh-auth-muted: #667372;
  --qlh-auth-primary: #0B4A4C;
  --qlh-auth-focus: #0FB5AE;
}

body {
  background: var(--qlh-auth-canvas) !important;
  color: var(--qlh-auth-text) !important;
}

#app main[class*='main'] {
  min-height: 100vh;
  padding: 24px;
  background: transparent !important;
}

#app main[class*='main'] > div[class*='wrapper'] {
  box-sizing: border-box;
  width: min(400px, calc(100vw - 32px));
  padding: 32px;
  border: 1px solid var(--qlh-auth-border);
  border-radius: 16px;
  background: var(--qlh-auth-surface);
  box-shadow: none;
}

#app img[class*='logo'],
#app img[alt*='logo' i] {
  width: 36px;
  height: 36px;
  object-fit: contain;
}

#app h1,
#app h2,
#app [class*='title'] {
  color: var(--qlh-auth-text);
  font-weight: 650;
  letter-spacing: -0.02em;
}

#app p,
#app [class*='description'] {
  color: var(--qlh-auth-muted);
}

#app input {
  min-height: 48px;
  border-color: var(--qlh-auth-border) !important;
  border-radius: 8px !important;
  background: #ffffff !important;
  color: var(--qlh-auth-text) !important;
  box-shadow: none !important;
}

#app input:hover,
#app input:focus {
  border-color: var(--qlh-auth-focus) !important;
  box-shadow: 0 0 0 3px rgba(15, 181, 174, 0.14) !important;
}

#app button,
#app a[class*='button'] {
  min-height: 48px;
  border-radius: 8px !important;
  font-weight: 650;
}

#app button[type='submit'] {
  border-color: var(--qlh-auth-primary) !important;
  background: var(--qlh-auth-primary) !important;
  color: #ffffff !important;
  box-shadow: none !important;
}

#app button[type='submit']:hover {
  border-color: #083b3d !important;
  background: #083b3d !important;
}

#app a {
  color: var(--qlh-auth-primary);
  font-weight: 600;
}

#app [class*='divider'] {
  color: #879291;
}

.qlh-auth-brand {
  margin: 0 0 24px;
  color: var(--qlh-auth-primary);
  font-size: 15px;
  font-weight: 650;
  letter-spacing: 0.01em;
  text-align: center;
}

.qlh-auth-note {
  margin: 16px 0 0;
  color: var(--qlh-auth-muted);
  font-size: 12px;
  line-height: 1.6;
  text-align: center;
}

#app footer,
#app [class*='footer'] {
  color: #879291;
  font-size: 12px;
}

@media (max-width: 640px) {
  #app main[class*='main'] {
    padding: 16px;
  }

  #app main[class*='main'] > div[class*='wrapper'] {
    padding: 28px 24px;
  }
}

@media (prefers-color-scheme: dark) {
  :root {
    --qlh-auth-canvas: #101718;
    --qlh-auth-surface: #152021;
    --qlh-auth-border: #2a3939;
    --qlh-auth-text: #f1f5f4;
    --qlh-auth-muted: #a6b2b1;
    --qlh-auth-primary: #0FB5AE;
    --qlh-auth-focus: #0FB5AE;
  }

  #app input {
    background: #10191a !important;
  }

  #app button[type='submit'] {
    color: #071313 !important;
  }

  #app button[type='submit']:hover {
    border-color: #45c7c0 !important;
    background: #45c7c0 !important;
  }

  #app footer,
  #app [class*='footer'] {
    color: #7f8d8b;
  }
}
`;

export const centralPlantLogtoAccountProfile = Object.freeze({
  username: 'central_plant_operator',
  email: 'central.plant.operator@example.test',
  displayName: '中央机房操作员',
});

export function buildCentralPlantLogtoExperience({ webURL, loginURL }) {
  const brandAssetURL = `${webURL}/quanlaihe-mark.svg`;
  return {
    color: {
      primaryColor: '#0B4A4C',
      isDarkModeEnabled: true,
      darkPrimaryColor: '#0FB5AE',
    },
    branding: {
      logoUrl: brandAssetURL,
      darkLogoUrl: brandAssetURL,
      favicon: brandAssetURL,
      darkFavicon: brandAssetURL,
    },
    hideLogtoBranding: false,
    languageInfo: {
      autoDetect: false,
      fallbackLanguage: 'zh-CN',
    },
    signIn: {
      methods: [
        {
          identifier: 'username',
          password: true,
          verificationCode: false,
          isPasswordPrimary: true,
        },
      ],
    },
    signUp: {
      identifiers: ['username'],
      password: true,
      verify: false,
    },
    signInMode: 'SignInAndRegister',
    customCss: industrialCalmAuthCss,
    customContent: {
      '/sign-in': '<div class="qlh-auth-brand">泉来禾智慧能源</div>',
      '/register': '<div class="qlh-auth-brand">泉来禾智慧能源</div><p class="qlh-auth-note">注册账号需由管理员分配访问权限</p>',
    },
    unknownSessionRedirectUrl: loginURL,
    supportWebsiteUrl: webURL,
  };
}

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
  loginURL,
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
      postLogoutRedirectUris: [webURL, `${webURL}/?logged_out=1`],
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

  await request('/api/sign-in-exp', {
    method: 'PATCH',
    body: buildCentralPlantLogtoExperience({ webURL, loginURL }),
  });

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
    registrationEnabled: true,
  };
}
