// The mini program's own modules import each other without an extension, the way WeChat's compiler
// resolves them; Node resolves those relative imports to the .ts file when it runs the tests.
import { registerHooks } from 'node:module';

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith('.') && !/\.[cm]?[jt]s$/.test(specifier)) {
      try {
        return nextResolve(`${specifier}.ts`, context);
      } catch {
        // Not a TypeScript module: let Node resolve it as written.
      }
    }
    return nextResolve(specifier, context);
  },
});
