import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCentralPlantLogtoExperience } from './central-plant-logto.mjs';

const webURL = 'https://127.0.0.1:58443';
const expectedReturnPath = '/sites/018f3e00-1000-7000-8000-000000000001/assets';
const loginURL = `${webURL}/api/v1/auth/login?returnTo=${encodeURIComponent(expectedReturnPath)}`;

test('central plant Logto experience enables safe self-registration and HVAC branding', () => {
  const experience = buildCentralPlantLogtoExperience({ webURL, loginURL });

  assert.deepEqual(experience.color, {
    primaryColor: '#087F76',
    isDarkModeEnabled: true,
    darkPrimaryColor: '#14B8A6',
  });
  assert.equal(experience.branding.logoUrl, `${webURL}/quanlaihe-mark.svg`);
  assert.equal(experience.branding.darkLogoUrl, `${webURL}/quanlaihe-mark.svg`);
  assert.equal(experience.branding.favicon, `${webURL}/quanlaihe-mark.svg`);
  assert.equal(experience.hideLogtoBranding, false);
  assert.deepEqual(experience.languageInfo, { autoDetect: false, fallbackLanguage: 'zh-CN' });
  assert.equal(experience.signInMode, 'SignInAndRegister');
  assert.deepEqual(experience.signIn.methods, [
    { identifier: 'username', password: true, verificationCode: false, isPasswordPrimary: true },
  ]);
  assert.deepEqual(experience.signUp, {
    identifiers: ['username'],
    password: true,
    verify: false,
  });
  assert.equal(experience.unknownSessionRedirectUrl, loginURL);
  assert.match(experience.customContent['/sign-in'], /泉来禾智慧能源/);
  assert.match(experience.customContent['/register'], /管理员审核后分配组织与站点权限/);
  assert.match(experience.customCss, /#087F76/);
  assert.match(experience.customCss, /border-radius:\s*20px/);
  assert.match(experience.customCss, /qlh-auth-intro/);
});
