import type {
  FindingSynthesisInput,
  FindingSynthesisProviderMetadata,
  FindingSynthesisProviderResponse,
  FindingSynthesizer,
} from '../../application/index.js';

export interface FakeFindingSynthesizerOptions {
  readonly output?: unknown | ((input: FindingSynthesisInput) => unknown | Promise<unknown>);
  readonly metadata?: Partial<FindingSynthesisProviderMetadata>;
  readonly error?: Error;
  readonly delayMs?: number;
}

export interface FakeFindingSynthesizer extends FindingSynthesizer {
  readonly calls: readonly FindingSynthesisInput[];
}

const pause = async (delayMs: number): Promise<void> => {
  if (delayMs <= 0) return;
  await new Promise<void>((resolve) => setTimeout(resolve, delayMs));
};

export const createFakeFindingSynthesizer = (
  options: FakeFindingSynthesizerOptions = {},
): FakeFindingSynthesizer => {
  const calls: FindingSynthesisInput[] = [];
  const descriptor = Object.freeze({
    provider: options.metadata?.provider ?? 'fake-provider',
    model: options.metadata?.model ?? 'fake-finding-synthesizer-v1',
    configurationDigest: options.metadata?.configurationDigest ?? `sha256:${'0'.repeat(64)}`,
  });
  const synthesizer: FakeFindingSynthesizer = {
    descriptor,
    calls,
    async synthesize(input): Promise<FindingSynthesisProviderResponse> {
      calls.push(input);
      await pause(options.delayMs ?? 0);
      if (options.error !== undefined) throw options.error;
      const output = typeof options.output === 'function'
        ? await options.output(input)
        : options.output ?? {
          classification: input.expectedClassification,
          statement: input.deterministicStatement,
          evidenceIds: input.evidence.map(({ id }) => id),
          limitations: [],
        };
      return Object.freeze({
        output,
        metadata: Object.freeze({
          provider: descriptor.provider,
          model: descriptor.model,
          configurationDigest: descriptor.configurationDigest,
          latencyMs: options.metadata?.latencyMs ?? (options.delayMs ?? 0),
          tokenUsage: options.metadata?.tokenUsage ?? null,
          traceId: options.metadata?.traceId ?? null,
        }),
      });
    },
  };
  return Object.freeze(synthesizer);
};
