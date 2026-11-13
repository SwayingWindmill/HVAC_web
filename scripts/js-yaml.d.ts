declare module 'js-yaml' {
  export interface LoadOptions {
    filename?: string;
    json?: boolean;
  }

  export function load(str: string, opts?: LoadOptions): unknown;
  export function dump(obj: unknown): string;
}
