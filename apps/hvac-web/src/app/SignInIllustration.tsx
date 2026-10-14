// The sign-in brand illustration: a building with a cooling tower on its roof, an engineer in a hard hat
// checking a tablet, and the plant's energy falling on a board beside them. Flat shapes in the design
// system's indigo with a teal and an amber accent; the fan turns, cool air rises, the bars breathe, the
// clouds and badges drift. Every motion is motion-safe, so it all stands still for reduced motion.
const SPIN = 'origin-center [transform-box:fill-box] motion-safe:animate-[ill-spin_2.8s_linear_infinite]';
const FLOAT = 'motion-safe:animate-[ill-float_3.2s_ease-in-out_infinite_alternate]';
const DRIFT = 'motion-safe:animate-[ill-drift_9s_ease-in-out_infinite_alternate]';
const SWAY = 'origin-bottom [transform-box:fill-box] motion-safe:animate-[ill-sway_3.6s_ease-in-out_infinite_alternate]';
const BAR = 'origin-bottom [transform-box:fill-box] motion-safe:animate-[ill-bar_2.6s_ease-in-out_infinite_alternate]';
const RISE = 'motion-safe:animate-[ill-rise_2.6s_ease-out_infinite]';
const DASH = 'motion-safe:animate-[ill-dash_1.6s_linear_infinite]';
const BLINK = 'motion-safe:animate-[ill-blink_4s_ease-in-out_infinite]';

const BARS = [72, 64, 68, 52, 46, 36];

export function SignInIllustration({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 640 520" fill="none" aria-hidden className={className}>
      {/* backdrop and ground */}
      <circle cx="330" cy="262" r="214" fill="var(--ill-blob)" />
      <ellipse cx="330" cy="470" rx="262" ry="14" fill="var(--ill-pale)" />

      {/* clouds */}
      <g className={DRIFT}>
        <path d="M86 132a14 14 0 0 1 10-24 18 18 0 0 1 34 4 12 12 0 0 1 6 20Z" fill="var(--ill-card)" />
      </g>
      <g className={DRIFT} style={{ animationDelay: '-4s' }}>
        <path d="M520 92a12 12 0 0 1 9-20 16 16 0 0 1 29 3 10 10 0 0 1 5 17Z" fill="var(--ill-card)" />
      </g>

      {/* building */}
      <rect x="112" y="212" width="146" height="256" rx="4" fill="var(--ill-soft)" />
      <rect x="248" y="212" width="18" height="256" rx="3" fill="var(--ill-primary)" />
      <rect x="104" y="202" width="168" height="12" rx="4" fill="var(--ill-ink)" />
      {Array.from({ length: 6 }, (_, row) => [0, 1, 2].map((column) => (
        <rect
          key={`${row}-${column}`}
          x={128 + column * 40}
          y={232 + row * 36}
          width="26"
          height="20"
          rx="3"
          fill={(row + column) % 4 === 1 ? 'var(--ill-amber)' : 'var(--ill-pale)'}
          className={(row + column) % 4 === 1 ? BLINK : undefined}
          style={(row + column) % 4 === 1 ? { animationDelay: `${-row * 0.7}s` } : undefined}
        />
      )))}
      <rect x="168" y="430" width="34" height="38" rx="3" fill="var(--ill-ink)" />

      {/* cooling tower on the roof, its fan turning and cool air rising from it */}
      <path d="M152 202 L228 202 L218 148 L162 148 Z" fill="var(--ill-primary)" />
      <rect x="160" y="162" width="60" height="5" rx="2.5" fill="var(--ill-soft)" />
      <rect x="157" y="176" width="66" height="5" rx="2.5" fill="var(--ill-soft)" />
      <rect x="155" y="190" width="70" height="5" rx="2.5" fill="var(--ill-soft)" />
      <rect x="166" y="136" width="48" height="13" rx="4" fill="var(--ill-ink)" />
      <circle cx="190" cy="122" r="17" fill="var(--ill-card)" />
      <g className={SPIN}>
        <path d="M190 122 C187 112 188 108 190 107 C195 109 195 115 190 122 Z" fill="var(--ill-ink)" />
        <path d="M190 122 C200 120 204 123 204 125 C201 129 195 128 190 122 Z" fill="var(--ill-ink)" />
        <path d="M190 122 C183 130 179 130 177 129 C177 124 183 121 190 122 Z" fill="var(--ill-ink)" />
      </g>
      <circle cx="190" cy="122" r="3" fill="var(--ill-amber)" />
      {[150, 190, 230].map((x, index) => (
        <path
          key={x}
          d={`M${x} 92 q-6 -8 0 -16 q6 -8 0 -16`}
          stroke="var(--ill-soft)"
          strokeWidth="3"
          strokeLinecap="round"
          className={RISE}
          style={{ animationDelay: `${index * 0.8}s` }}
        />
      ))}

      {/* data flowing from the building to the board */}
      <path d="M268 250 C306 250 312 170 352 170" stroke="var(--ill-primary)" strokeWidth="2.5" strokeLinecap="round" strokeDasharray="4 8" className={DASH} />

      {/* the energy board: falling bars and a falling trend */}
      <g transform="translate(0 -44)">
      <g className={FLOAT}>
        <rect x="350" y="124" width="214" height="152" rx="16" fill="var(--ill-ink)" opacity="0.08" transform="translate(6 10)" />
        <rect x="350" y="124" width="214" height="152" rx="16" fill="var(--ill-card)" />
        <circle cx="374" cy="148" r="10" fill="var(--ill-green)" />
        <path d="M370 151 C370 144 375 141 380 141 C380 147 376 151 370 151 Z M370 151 L376 145" fill="var(--ill-card)" stroke="var(--ill-card)" strokeWidth="1.2" strokeLinecap="round" />
        <rect x="392" y="142" width="64" height="6" rx="3" fill="var(--ill-ink)" />
        <rect x="392" y="152" width="40" height="5" rx="2.5" fill="var(--ill-pale)" />
        {BARS.map((height, index) => (
          <rect
            key={index}
            x={372 + index * 30}
            y={258 - height}
            width="16"
            height={height}
            rx="4"
            fill={index === BARS.length - 1 ? 'var(--ill-green)' : index % 2 ? 'var(--ill-soft)' : 'var(--ill-primary)'}
            className={BAR}
            style={{ animationDelay: `${-index * 0.35}s` }}
          />
        ))}
        <path d="M380 190 L410 198 L440 194 L470 212 L500 218 L530 228" stroke="var(--ill-amber)" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
        <circle cx="530" cy="228" r="5" fill="var(--ill-amber)" className="motion-safe:animate-pulse" />
      </g>
      </g>

      {/* the engineer: hard hat, jacket with a reflective band, tablet in hand */}
      <g>
        <rect x="438" y="378" width="21" height="86" rx="9" fill="var(--ill-primary)" />
        <rect x="463" y="378" width="21" height="86" rx="9" fill="var(--ill-soft)" />
        <rect x="428" y="456" width="34" height="12" rx="6" fill="var(--ill-ink)" />
        <rect x="462" y="456" width="34" height="12" rx="6" fill="var(--ill-ink)" />
        <path d="M486 318 L496 360 L476 366" stroke="var(--ill-ink)" strokeWidth="15" strokeLinecap="round" strokeLinejoin="round" opacity="0.85" />
        <rect x="430" y="300" width="62" height="96" rx="20" fill="var(--ill-ink)" />
        <rect x="430" y="350" width="62" height="7" fill="var(--ill-amber)" />
        <rect x="453" y="286" width="16" height="18" rx="5" fill="var(--ill-skin)" />
        <circle cx="461" cy="272" r="20" fill="var(--ill-skin)" />
        <path d="M466 258 C482 260 484 280 474 288 C472 278 470 268 466 258 Z" fill="var(--ill-ink)" />
        <circle cx="470" cy="274" r="4" fill="var(--ill-skin)" />
        <path d="M439 266 A22 22 0 0 1 483 266 Z" fill="var(--ill-amber)" />
        <rect x="430" y="262" width="58" height="7" rx="3.5" fill="var(--ill-amber-deep)" />
        <rect x="394" y="314" width="34" height="48" rx="6" fill="var(--ill-ink)" transform="rotate(-12 411 338)" />
        <rect x="399" y="320" width="24" height="34" rx="3" fill="var(--ill-soft)" transform="rotate(-12 411 338)" />
        <path d="M440 316 L422 350 L408 346" stroke="var(--ill-ink)" strokeWidth="15" strokeLinecap="round" strokeLinejoin="round" />
        <circle cx="406" cy="345" r="7" fill="var(--ill-skin)" />
      </g>

      {/* floating badges: saved energy and cooling */}
      <g className={FLOAT} style={{ animationDelay: '-1.4s' }}>
        <circle cx="304" cy="150" r="20" fill="var(--ill-amber)" />
        <path d="M307 136 L296 153 L304 153 L300 165 L313 147 L305 147 Z" fill="var(--ill-card)" />
      </g>
      <g className={FLOAT} style={{ animationDelay: '-2.6s' }}>
        <circle cx="586" cy="330" r="18" fill="var(--ill-green)" />
        <path d="M586 318 V342 M576 324 L596 336 M596 324 L576 336" stroke="var(--ill-card)" strokeWidth="2.6" strokeLinecap="round" />
      </g>

      {/* plants */}
      <g className={SWAY}>
        <path d="M80 468 C66 440 70 414 84 398 C92 420 90 446 80 468 Z" fill="var(--ill-green)" />
        <path d="M82 468 C92 446 108 436 122 434 C116 452 100 464 82 468 Z" fill="var(--ill-green)" opacity="0.7" />
      </g>
      <g className={SWAY} style={{ animationDelay: '-1.8s' }}>
        <path d="M566 468 C556 448 560 430 572 418 C578 436 576 452 566 468 Z" fill="var(--ill-green)" opacity="0.8" />
        <path d="M568 468 C576 452 590 446 600 446 C594 460 582 466 568 468 Z" fill="var(--ill-green)" />
      </g>
    </svg>
  );
}
