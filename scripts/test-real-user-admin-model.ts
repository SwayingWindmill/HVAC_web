import assert from 'node:assert/strict';
import test from 'node:test';
import {
  userAdminMemberSchema,
  type BuiltInRoleKey,
  type UserAdminMember,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  memberSubmitsHighRiskCommands,
  resolveMemberTwoFactorDisplay,
  roleSubmitsHighRiskCommands,
} from '../apps/hvac-web/src/features/system/users/user-admin-model.ts';

const MEMBER_ID = '018f3e00-2000-7000-8000-000000000010';
const BINDING_ID = '018f3e00-2200-7000-8000-000000000010';
const SITE_ID = '018f3e00-1000-7000-8000-000000000001';

function sampleMember(overrides: Partial<UserAdminMember> = {}): UserAdminMember {
  return {
    principalId: MEMBER_ID,
    username: 'duty.lead',
    displayName: '值班主管',
    email: 'duty.lead@example.invalid',
    enabled: true,
    locked: false,
    mfaEnabled: false,
    self: false,
    assignments: [{ bindingId: BINDING_ID, roleKey: 'site-manager', siteId: SITE_ID }],
    ...overrides,
  };
}

test('only admin and site-manager roles can submit high-risk or critical commands', () => {
  const cases: readonly (readonly [BuiltInRoleKey, boolean])[] = [
    ['viewer', false],
    ['operator', false],
    ['site-manager', true],
    ['admin', true],
  ];
  for (const [roleKey, expected] of cases) {
    assert.equal(roleSubmitsHighRiskCommands(roleKey), expected);
  }
});

test('two-factor display marks active high-risk submitters without MFA and clears warning when disabled', () => {
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({ mfaEnabled: true })),
    { kind: 'enabled', label: '已开通' },
  );
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({ mfaEnabled: false })),
    { kind: 'missing_for_high_risk', label: '未开通', warning: '提交不了高风险指令' },
  );
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({
      mfaEnabled: false,
      assignments: [{ bindingId: BINDING_ID, roleKey: 'admin' }],
    })),
    { kind: 'missing_for_high_risk', label: '未开通', warning: '提交不了高风险指令' },
  );
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({
      mfaEnabled: false,
      assignments: [{ bindingId: BINDING_ID, roleKey: 'operator', siteId: SITE_ID }],
    })),
    { kind: 'not_enabled', label: '未开通' },
  );
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({
      mfaEnabled: false,
      assignments: [
        { bindingId: BINDING_ID, roleKey: 'viewer', siteId: SITE_ID },
        { bindingId: '018f3e00-2200-7000-8000-000000000011', roleKey: 'site-manager', siteId: SITE_ID },
      ],
    })),
    { kind: 'missing_for_high_risk', label: '未开通', warning: '提交不了高风险指令' },
  );
  assert.equal(memberSubmitsHighRiskCommands(sampleMember({ enabled: false })), false);
  assert.deepEqual(
    resolveMemberTwoFactorDisplay(sampleMember({ enabled: false, mfaEnabled: false })),
    { kind: 'not_enabled', label: '未开通' },
  );
});

test('userAdminMemberSchema requires mfaEnabled boolean and rejects secret fields', () => {
  assert.equal(userAdminMemberSchema.safeParse(sampleMember({ mfaEnabled: true })).success, true);

  const missingMfa: Record<string, unknown> = { ...sampleMember() };
  delete missingMfa.mfaEnabled;
  assert.equal(userAdminMemberSchema.safeParse(missingMfa).success, false);

  assert.equal(
    userAdminMemberSchema.safeParse({ ...sampleMember(), provisioningKey: 'SECRET' }).success,
    false,
  );
  assert.equal(
    userAdminMemberSchema.safeParse({ ...sampleMember(), ciphertext: 'deadbeef' }).success,
    false,
  );
});
