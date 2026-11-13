export interface GoWorkspace {
  goVersion: string;
  modules: string[];
}

export function readGoWorkspace(root: string): GoWorkspace;

export function installGoTool(options: {
  root: string;
  goBinary: string;
  goCacheDir: string;
  goVersion: string;
  name: string;
  packagePath: string;
  version: string;
}): string;
