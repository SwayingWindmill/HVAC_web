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

function readPalette(): ScenePalette {
  const style = getComputedStyle(document.documentElement);
  const read = (token: string): string => style.getPropertyValue(token).trim();
  return {
    background: read('--background'),
    card: read('--card'),
    border: read('--border'),
    muted: read('--muted'),
    foreground: read('--foreground'),
    chilled: read('--primary'),
    cooling: read('--chart-2'),
    accent: read('--chart-2'),
    running: read('--success'),
    fault: read('--destructive'),
    inactive: read('--chart-inactive'),
  };
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
