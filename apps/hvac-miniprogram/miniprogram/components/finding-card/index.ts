// The card under an answer (lib/assistant.ts presentFinding): whether the data supports it, its
// chart, the numbers it rests on and what the data could not tell, as the web's FindingCard.
Component({
  options: { virtualHost: true, styleIsolation: 'apply-shared' },
  properties: {
    finding: { type: Object, value: {} },
  },
});
