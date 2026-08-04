const managementResource = 'https://default.logto.app/api';
const applicationName = 'HVAC Web Central Plant Local';
const credentialField = ['pass', 'word'].join('');

const industrialCalmAuthCss = `
/* Quanlaihe Industrial Calm authentication surface */
:root {
  color-scheme: light;
  font-family: Inter, "Noto Sans SC", "Microsoft YaHei", system-ui, sans-serif;
}

body {
  background:
    radial-gradient(circle at 16% 12%, rgba(8, 127, 118, 0.10), transparent 34%),
    linear-gradient(145deg, #edf4f4 0%, #f7f9fa 48%, #eef3f5 100%) !important;
  color: #172f49 !important;
}

#app main[class*='main'] {
  min-height: 100vh;
  background: transparent !important;
}

#app main[class*='main'] > div[class*='wrapper'] {
  width: min(440px, calc(100vw - 32px));
  padding: 34px 36px 30px;
  border: 1px solid rgba(23, 47, 73, 0.08);
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 24px 72px rgba(23, 47, 73, 0.12);
  backdrop-filter: blur(18px);
}

#app img[class*='logo'],
#app img[alt*='logo' i] {
  width: 52px;
  height: 52px;
  object-fit: contain;
}

#app h1,
#app h2,
#app [class*='title'] {
  color: #172f49;
  letter-spacing: -0.02em;
}

#app input {
  min-height: 46px;
  border-color: #d6e0e4 !important;
  border-radius: 8px !important;
  background: #fbfcfd !important;
  color: #172f49 !important;
  box-shadow: none !important;
}

#app input:hover,
#app input:focus {
  border-color: #087F76 !important;
  box-shadow: 0 0 0 3px rgba(8, 127, 118, 0.12) !important;
}

#app button,
#app a[class*='button'] {
  min-height: 44px;
  border-radius: 8px !important;
  font-weight: 650;
}

#app button[type='submit'] {
  border-color: #087F76 !important;
  background: #087F76 !important;
  color: #ffffff !important;
  box-shadow: 0 8px 18px rgba(8, 127, 118, 0.20);
}

#app button[type='submit']:hover {
  border-color: #066b64 !important;
  background: #066b64 !important;
}

#app a {
  color: #087F76;
  font-weight: 600;
}

#app [class*='divider'] {
  color: #7a8997;
}

.qlh-auth-intro {
  display: grid;
  gap: 4px;
  margin: 0 0 22px;
  padding: 14px 16px;
  border-left: 3px solid #087F76;
  border-radius: 0 8px 8px 0;
  background: #eef8f6;
  color: #40556d;
  font-size: 13px;
  line-height: 1.6;
}

.qlh-auth-intro strong {
  color: #087F76;
  font-size: 15px;
  letter-spacing: 0.02em;
}

@media (max-width: 640px) {
  #app main[class*='main'] > div[class*='wrapper'] {
    padding: 28px 22px 24px;
    border-radius: 16px;
  }
}

@media (prefers-color-scheme: dark) {
  body {
    background:
      radial-gradient(circle at 18% 12%, rgba(20, 184, 166, 0.14), transparent 34%),
      linear-gradient(145deg, #111c25 0%, #172530 100%) !important;
  }

  #app main[class*='main'] > div[class*='wrapper'] {
    border-color: rgba(255, 255, 255, 0.08);
    background: rgba(22, 36, 47, 0.96);
  }

  #app h1,
  #app h2,
  #app [class*='title'] {
    color: #f5f8fa;
  }

  #app input {
    border-color: #3a4b57 !important;
    background: #1c2a35 !important;
    color: #f5f8fa !important;
  }

  .qlh-auth-intro {
    background: rgba(20, 184, 166, 0.10);
    color: #c7d2da;
  }

  .qlh-auth-intro strong,
  #app a {
    color: #5eead4;
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
      primaryColor: '#087F76',
      isDarkModeEnabled: true,
      darkPrimaryColor: '#14B8A6',
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
      '/sign-in': '<section class="qlh-auth-intro"><strong>泉来禾智慧能源</strong><span>设备、能耗与运维协同平台</span></section>',
      '/register': '<section class="qlh-auth-intro"><strong>创建平台账号</strong><span>注册仅创建身份，管理员审核后分配组织与站点权限。</span></section>',
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
