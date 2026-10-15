import { useMemo, type ReactNode } from 'react';
import { useFrame, useThree } from '@react-three/fiber';
import * as THREE from 'three';
import type { V3 as Vec } from './assets/kit';

// Labels live in one DOM layer over the canvas, not in the scene: each rendered frame projects every
// anchor to the screen and moves its element directly (no React render, no root per label). Labels
// keep a constant size, sit nearer-on-top, and hide when behind the camera.

export interface LabelStore {
  readonly anchors: Map<string, Vec>;
  readonly elements: Map<string, HTMLElement>;
  /** Set by the projector: asks the scene for a frame, so a label that just appeared gets placed. */
  invalidate: () => void;
}

export function useLabelStore(): LabelStore {
  return useMemo(() => ({ anchors: new Map(), elements: new Map(), invalidate: () => undefined }), []);
}

/** Inside the canvas: projects the anchors on each frame the scene draws. */
export function LabelProjector({ store }: { readonly store: LabelStore }) {
  const point = useMemo(() => new THREE.Vector3(), []);
  store.invalidate = useThree((state) => state.invalidate);
  useFrame(({ camera, size }) => {
    for (const [id, element] of store.elements) {
      const anchor = store.anchors.get(id);
      if (!anchor) continue;
      point.set(anchor[0], anchor[1], anchor[2]).project(camera);
      // Labels whose anchor nears the top edge would be cut off by the panel; they hide instead.
      const visible = point.z < 1 && point.x > -1 && point.x < 1 && point.y > -1 && point.y < 0.8;
      element.style.visibility = visible ? 'visible' : 'hidden';
      if (!visible) continue;
      element.style.transform = `translate3d(${((point.x + 1) / 2) * size.width}px, ${((1 - point.y) / 2) * size.height}px, 0)`;
      element.style.zIndex = String(Math.round((1 - point.z) * 100000));
    }
  });
  return null;
}

/** Outside the canvas: one positioned element per label, its anchor point at its bottom centre. */
export function Label({ store, id, anchor, children, interactive = false }: {
  readonly store: LabelStore;
  readonly id: string;
  readonly anchor: Vec;
  readonly children: ReactNode;
  readonly interactive?: boolean;
}) {
  store.anchors.set(id, anchor);
  return (
    <div
      ref={(element) => {
        if (element) {
          store.elements.set(id, element);
          store.invalidate();
        } else {
          store.elements.delete(id);
        }
      }}
      className="absolute top-0 left-0 will-change-transform"
      style={{ visibility: 'hidden', pointerEvents: 'none' }}
    >
      <div className={interactive ? 'pointer-events-auto absolute bottom-0 left-0 -translate-x-1/2' : 'absolute bottom-0 left-0 -translate-x-1/2'}>{children}</div>
    </div>
  );
}
