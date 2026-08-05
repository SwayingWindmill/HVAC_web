import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCentralPlantLogtoExperience } from './central-plant-logto.mjs';

const webURL = 'https://127.0.0.1:58443';
const expectedReturnPath = '/sites/018f3e00-1000-7000-8000-000000000001/assets';
const loginURL = `${webURL}/api/v1/auth/login?returnTo=${encodeURIComponent(expectedReturnPath)}`;

test('central plant Logto experience enables safe self-registration and minimal HVAC branding', () => {
  const experience = buildCentralPlantLogtoExperience({ webURL, loginURL });

  assert.deepEqual(experience.color, {
    primaryColor: '#0B4A4C',
    isDarkModeEnabled: true,
    darkPrimaryColor: '#0FB5AE',
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
  assert.doesNotMatch(experience.customContent['/sign-in'], /设备|能耗|运维协同|建筑|区域|点位/);
  assert.match(experience.customContent['/register'], /注册账号需由管理员分配访问权限/);
  assert.doesNotMatch(experience.customContent['/register'], /设备|能耗|运维协同|建筑|区域|点位/);
  assert.match(experience.customCss, /#0B4A4C/);
  assert.match(experience.customCss, /border-radius:\s*16px/);
  assert.match(experience.customCss, /border-radius:\s*8px/);
  assert.match(experience.customCss, /width:\s*36px/);
  assert.match(experience.customCss, /box-shadow:\s*none/);
  assert.match(experience.customCss, /qlh-auth-brand/);
  assert.match(experience.customCss, /qlh-auth-note/);
  assert.doesNotMatch(experience.customCss, /gradient\(/);
  assert.doesNotMatch(experience.customCss, /backdrop-filter/);
  assert.doesNotMatch(experience.customCss, /qlh-auth-intro/);
});
