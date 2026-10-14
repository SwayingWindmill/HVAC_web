import { useId } from 'react';

// The 泉来禾 mark: four pinwheel blades parted by a thin cross, for moving air and clean energy. One colour,
// the current text colour, so it reads on light and dark alike; public/quanlaihe-mark.svg is the same
// drawing for the browser tab.
export function BrandMark({ className }: { className?: string }) {
  const id = useId();
  return (
    <svg viewBox="0 0 64 64" fill="currentColor" aria-hidden className={className}>
      <defs>
        <mask id={`${id}-gap`}>
          <rect width="64" height="64" fill="#fff" />
          <path d="M32 2V62M2 32H62" stroke="#000" strokeWidth="3" />
        </mask>
      </defs>
      <g mask={`url(#${id}-gap)`}>
        {[0, 90, 180, 270].map((angle) => (
          <path key={angle} d="M32 32V5C43 5 50 12.5 50 21C50 27.5 43 32 32 32Z" transform={`rotate(${angle} 32 32)`} />
        ))}
      </g>
    </svg>
  );
}
