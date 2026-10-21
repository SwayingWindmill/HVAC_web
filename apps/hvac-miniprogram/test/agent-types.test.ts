import assert from 'node:assert/strict';
import test from 'node:test';
import type * as Web from '../../hvac-web/src/api/generated/operationsAgentSessions.gen.ts';
import type * as Phone from '../miniprogram/lib/agent-types.ts';

// The web's types come from the contract's generated schemas; the mini program writes its own,
// read-only. The type check fails when the two drift apart in either direction.
type ReadOnly<T> = T extends readonly (infer Item)[] ? readonly ReadOnly<Item>[] : T extends object ? { readonly [Key in keyof T]: ReadOnly<T[Key]> } : T;
type Same<Left, Right> = [Left] extends [Right] ? ([Right] extends [Left] ? true : false) : false;

const sameAsTheWeb: readonly true[] = [
  true satisfies Same<ReadOnly<Web.AgentSessionSnapshot>, Phone.AgentSessionSnapshot>,
  true satisfies Same<ReadOnly<Web.AgentSessionEvent>, Phone.AgentSessionEvent>,
  true satisfies Same<ReadOnly<Web.AgentArtifact>, Phone.AgentArtifact>,
  true satisfies Same<ReadOnly<Web.FindingChart>, Phone.FindingChart>,
];

test('the mini program reads Agent Sessions with the same types as the web', () => {
  assert.equal(sameAsTheWeb.length, 4);
});
