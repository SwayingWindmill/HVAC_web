import assert from 'node:assert/strict';
import test from 'node:test';
import { hasConfiguredDeepSeekApiKey } from './lib/local-runtime-env.ts';

test('returns false when DEEPSEEK_API_KEY is missing, empty, or placeholder', () => {
  assert.equal(hasConfiguredDeepSeekApiKey('HVAC_ENV=development\n', {}), false);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=\n', {}), false);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=""\n', {}), false);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=[REDACTED_SECRET]\n', {}), false);
});

test('returns true when DEEPSEEK_API_KEY is configured in env file or processEnv', () => {
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=sk-local-test-123\n', {}), true);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY="sk-local-test-123"\n', {}), true);
  assert.equal(hasConfiguredDeepSeekApiKey('', { DEEPSEEK_API_KEY: 'sk-process-123' }), true);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=sk-file\n', { DEEPSEEK_API_KEY: '' }), false);
  assert.equal(hasConfiguredDeepSeekApiKey('DEEPSEEK_API_KEY=sk-file\n', { DEEPSEEK_API_KEY: '[REDACTED_SECRET]' }), false);
});
