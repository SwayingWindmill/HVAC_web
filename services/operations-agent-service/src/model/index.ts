import { applicationModule } from '../application/index.js';

export {
  createFakeFindingSynthesizer,
  type FakeFindingSynthesizer,
  type FakeFindingSynthesizerOptions,
} from './internal/fake-finding-synthesizer.js';

export {
  OpenAiFindingSynthesizerError,
  createOpenAiFindingSynthesizer,
  type OpenAiFindingSynthesizer,
  type OpenAiFindingSynthesizerOptions,
} from './internal/openai-finding-synthesizer.js';

export const modelModule = Object.freeze({
  name: 'model',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type ModelModule = typeof modelModule;
