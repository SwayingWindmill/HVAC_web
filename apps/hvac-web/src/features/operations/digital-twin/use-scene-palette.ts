import { useEffect, useState } from 'react';

// The scene's colours are the design system's tokens, re-read when the theme class on <html> changes,
// so the 3D view follows light and dark like the rest of the app.
export interface ScenePalette {
  readonly background: string;
  readonly card: string;
  readonly border: string;
  readonly muted: string;
  readonly foreground: string;
  readonly chilled: string;
  readonly cooling: string;
  readonly accent: string;
  readonly running: string;
  readonly fault: string;
  readonly inactive: string;
}

const TOKENS: Readonly<Record<keyof ScenePalette, string>> = {
  background: '--background',
  card: '--card',
  border: '--border',
  muted: '--muted',
  foreground: '--foreground',
  chilled: '--primary',
  cooling: '--chart-2',
  accent: '--chart-2',
  running: '--success',
  fault: '--destructive',
  inactive: '--chart-inactive',
};

function readPalette(): ScenePalette {
  const style = getComputedStyle(document.documentElement);
  return Object.fromEntries(
    Object.entries(TOKENS).map(([name, token]) => [name, style.getPropertyValue(token).trim()]),
  ) as unknown as ScenePalette;
}

export function useScenePalette(): ScenePalette {
  const [palette, setPalette] = useState(readPalette);
  useEffect(() => {
    const observer = new MutationObserver(() => setPalette(readPalette()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'data-theme', 'style'] });
    return () => observer.disconnect();
  }, []);
  return palette;
}
