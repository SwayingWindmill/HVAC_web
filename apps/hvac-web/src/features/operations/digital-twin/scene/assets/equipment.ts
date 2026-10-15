import * as THREE from 'three';
import { addFins, addFlange, addGauge, addValve, box, dome, drum, drumX, drumZ, Kit, lathe, ring, torus, tube, type Asset, type V3 } from './kit';

// The HVAC equipment library, modelled from the machines on site at their real sizes (metres):
// a water-cooled screw chiller, an end-suction pump, a round counterflow cooling tower, and on the air
// side an air handling unit, a concealed fan coil, a VAV box and a four-way ceiling diffuser. Parts
// that turn (tower fan, pump coupling) are separate assets so they can spin without rebuilding.
// Built lazily, once per page load, and shared by every instance.

const HALF_PI = Math.PI / 2;
const TO_X: V3 = [0, 0, -HALF_PI]; // turns a +y feature to face +x
const TO_NEG_X: V3 = [0, 0, HALF_PI];
const TO_NEG_Z: V3 = [-HALF_PI, 0, 0];

/** Water-cooled screw chiller, 3.8 × 1.4 m, 1.9 m to the cabinet top. Nozzles at -x, cabinet at +x. */
function buildChiller(): Asset {
  const kit = new Kit();
  // Skid: two channel rails and cross members.
  for (const z of [-0.55, 0.55]) kit.add('paintDark', box(3.72, 0.16, 0.14, 0.012), { at: [0, 0.08, z] });
  for (const x of [-1.62, -0.42, 0.78]) kit.add('paintDark', box(0.12, 0.12, 1.0, 0.01), { at: [x, 0.07, 0] });

  const shells = [
    { z: 0.33, y: 0.58, r: 0.3, skin: 'insulation' as const, ports: ['chwIn', 'chwOut'] as const },
    { z: -0.33, y: 0.55, r: 0.27, skin: 'brand' as const, ports: ['cwIn', 'cwOut'] as const },
  ];
  for (const shell of shells) {
    for (const x of [-1.05, 0.85]) kit.add('paint', box(0.2, 0.3, 0.52, 0.02), { at: [x, 0.3, shell.z] });
    kit.add(shell.skin, drumX(shell.r, 2.84, 0.02, 40), { at: [-0.15, shell.y, shell.z] });
    if (shell.skin === 'insulation') {
      for (const x of [-1.0, -0.1, 0.8]) kit.add('steel', ring(shell.r + 0.008, shell.r - 0.01, 0.035, 40), { at: [x, shell.y, shell.z], rotate: TO_X });
    }
    addFlange(kit, 'paint', shell.r + 0.06, { at: [-1.57, shell.y, shell.z], rotate: TO_X }, 16);
    addFlange(kit, 'paint', shell.r + 0.06, { at: [1.27, shell.y, shell.z], rotate: TO_X }, 16);
    kit.add('paint', dome(shell.r + 0.02, 0.2, 36), { at: [-1.6, shell.y, shell.z], rotate: TO_NEG_X });
    kit.add('paint', dome(shell.r + 0.02, 0.1, 36), { at: [1.3, shell.y, shell.z], rotate: TO_X });
    // Two nozzles out of the water box: entering low, leaving high.
    shell.ports.forEach((port, index) => {
      const y = shell.y + (index === 0 ? -0.14 : 0.14);
      kit.add('paint', drumX(0.075, 0.34, 0.01, 24), { at: [-1.88, y, shell.z] });
      addFlange(kit, 'paint', 0.13, { at: [-2.06, y, shell.z], rotate: TO_NEG_X });
      addGauge(kit, 0.07, { at: [-1.92, y + 0.07, shell.z] });
      kit.ports[port] = [-2.08, y, shell.z];
    });
  }

  // Compressor and motor on a frame across both shells; oil separator over the condenser.
  for (const x of [-0.75, 0.6]) kit.add('paintDark', box(0.12, 0.22, 1.02, 0.015), { at: [x, 0.96, 0] });
  kit.add('paintDark', drumX(0.21, 0.98, 0.04, 36), { at: [-0.45, 1.24, 0] });
  kit.add('paintDark', drumX(0.245, 0.1, 0.02, 36), { at: [-0.97, 1.24, 0] });
  kit.add('paintDark', drumX(0.245, 0.1, 0.02, 36), { at: [0.06, 1.24, 0] });
  kit.add('paintDark', drumX(0.235, 0.68, 0.04, 36), { at: [0.45, 1.24, 0] });
  addFins(kit, 'paintDark', 0.23, 0.6, 18, 0.03, { at: [0.45, 1.24, 0] });
  kit.add('paintDark', dome(0.2, 0.09, 32), { at: [0.79, 1.24, 0], rotate: TO_X });
  kit.add('paintDark', box(0.28, 0.16, 0.24, 0.025), { at: [0.45, 1.52, 0] });
  kit.add('paint', drum(0.16, 0.6, 0.02, 32), { at: [-1.2, 0.84, -0.33] });
  kit.add('paint', dome(0.16, 0.08, 32), { at: [-1.2, 1.44, -0.33] });
  kit.add('steel', tube([[-0.98, 1.42, 0], [-0.98, 1.66, 0], [-1.2, 1.66, -0.2], [-1.2, 1.52, -0.33]], 0.048, 12, 0.08));
  kit.add('insulation', tube([[1.05, 0.87, 0.33], [1.05, 1.24, 0.33], [1.05, 1.24, 0.12], [0.84, 1.24, 0.0]], 0.07, 14, 0.1));
  kit.add('steel', tube([[0.95, 0.3, -0.33], [0.95, 0.22, -0.1], [0.95, 0.22, 0.12], [0.95, 0.3, 0.33]], 0.022, 8, 0.06));
  kit.add('steel', box(0.22, 0.12, 0.012, 0.004), { at: [0.2, 0.62, 0.635] });

  // Control cabinet at +x: two doors, handles, HMI, louvres, a drip cap.
  kit.add('panel', box(0.54, 1.72, 1.24, 0.03), { at: [1.64, 1.02, 0] });
  kit.add('paint', box(0.58, 0.05, 1.28, 0.02), { at: [1.64, 1.905, 0] });
  for (const z of [-0.305, 0.305]) kit.add('paint', box(0.024, 1.56, 0.58, 0.008), { at: [1.918, 1.02, z] });
  for (const z of [-0.05, 0.05]) kit.add('steel', box(0.03, 0.16, 0.03, 0.01), { at: [1.94, 1.04, z] });
  kit.add('paintDark', box(0.014, 0.27, 0.36, 0.01), { at: [1.932, 1.42, 0.3] });
  kit.add('screen', box(0.01, 0.22, 0.31, 0.004), { at: [1.94, 1.42, 0.3] });
  for (let index = 0; index < 5; index += 1) kit.add('grille', box(0.02, 0.022, 0.4, 0.006), { at: [1.935, 0.36 + index * 0.06, -0.3] });
  kit.ports.led = [1.936, 1.66, 0.3];
  return kit.build();
}

/** End-suction pump on a baseplate and concrete pad, 1.9 m long; suction at -x, discharge up. */
function buildPump(): Asset {
  const kit = new Kit();
  kit.add('concrete', box(1.94, 0.18, 0.8, 0.03), { at: [0.03, 0.09, 0] });
  kit.add('paintDark', box(1.74, 0.09, 0.52, 0.012), { at: [0.05, 0.225, 0] });
  const axis = 0.64;
  // Volute: casing, offset scroll, cover flange, foot, nozzles.
  kit.add('brand', drumX(0.25, 0.2, 0.05, 40), { at: [-0.5, axis, 0] });
  kit.add('brand', drumX(0.27, 0.15, 0.05, 40), { at: [-0.5, axis - 0.02, 0.04] });
  addFlange(kit, 'paint', 0.24, { at: [-0.39, axis, 0], rotate: TO_X }, 12);
  kit.add('paint', box(0.24, 0.24, 0.3, 0.02), { at: [-0.5, 0.39, 0] });
  kit.add('paint', drum(0.08, 0.36, 0.01, 24), { at: [-0.5, axis + 0.1, 0.14] });
  addFlange(kit, 'paint', 0.14, { at: [-0.5, axis + 0.47, 0.14] });
  kit.add('paint', drumX(0.09, 0.32, 0.01, 24), { at: [-0.76, axis, 0] });
  addFlange(kit, 'paint', 0.15, { at: [-0.93, axis, 0], rotate: TO_NEG_X });
  addGauge(kit, 0.08, { at: [-0.5, axis + 0.3, 0.22], rotate: [HALF_PI * 0.5, 0, 0] });
  kit.ports.suction = [-0.95, axis, 0];
  kit.ports.discharge = [-0.5, axis + 0.49, 0.14];
  // Bearing frame on its pedestal; an open coupling guard (a trough) so the turning coupling shows.
  kit.add('brand', drumX(0.11, 0.36, 0.02, 28), { at: [-0.18, axis, 0] });
  kit.add('brand', box(0.22, 0.34, 0.26, 0.02), { at: [-0.18, 0.43, 0] });
  const trough = new THREE.CylinderGeometry(0.17, 0.17, 0.3, 24, 1, true, 0, Math.PI);
  trough.rotateZ(-HALF_PI);
  kit.add('steel', trough, { at: [0.1, axis, 0] });
  kit.add('steel', box(0.3, 0.2, 0.3, 0.02), { at: [0.1, 0.37, 0] });
  // Motor: frame, cooling fins, flange, fan cowl with grille, terminal box, feet.
  kit.add('paintDark', drumX(0.205, 0.6, 0.03, 36), { at: [0.55, axis, 0] });
  addFins(kit, 'paintDark', 0.2, 0.52, 20, 0.032, { at: [0.55, axis, 0] });
  kit.add('paintDark', drumX(0.23, 0.05, 0.015, 36), { at: [0.26, axis, 0] });
  kit.add('paintDark', drumX(0.215, 0.15, 0.06, 36), { at: [0.92, axis, 0] });
  const grille = new THREE.CircleGeometry(0.16, 28);
  grille.rotateY(HALF_PI);
  kit.add('grille', grille, { at: [0.997, axis, 0] });
  kit.add('paintDark', box(0.18, 0.13, 0.18, 0.02), { at: [0.5, axis + 0.26, 0] });
  kit.add('steel', tube([[0.5, axis + 0.26, 0.09], [0.5, axis + 0.26, 0.2], [0.5, 0.3, 0.2]], 0.014, 8, 0.05));
  kit.add('paintDark', box(0.5, 0.16, 0.36, 0.015), { at: [0.55, 0.35, 0] });
  return kit.build();
}

/** The pump's turning coupling: shaft and two bolted hubs, about x through the origin. */
function buildCoupling(): Asset {
  const kit = new Kit();
  kit.add('steel', drumX(0.035, 0.34, 0.005, 16));
  kit.add('darkSteel', drumX(0.1, 0.07, 0.01, 24), { at: [-0.045, 0, 0] });
  kit.add('darkSteel', drumX(0.1, 0.07, 0.01, 24), { at: [0.045, 0, 0] });
  for (let index = 0; index < 6; index += 1) {
    const angle = (index / 6) * Math.PI * 2;
    kit.add('steel', drumX(0.014, 0.18, 0.003, 8), { at: [0, Math.cos(angle) * 0.075, Math.sin(angle) * 0.075] });
  }
  kit.add('paint', box(0.06, 0.03, 0.03, 0.005), { at: [0, 0.1, 0] });
  return kit.build();
}

const TOWER_BODY: readonly (readonly [number, number])[] = [[1.8, 1.62], [1.76, 1.85], [1.69, 2.1], [1.6, 2.35], [1.5, 2.6], [1.41, 2.82], [1.34, 2.98]];

/** Round counterflow cooling tower, 3.6 m across, 3.7 m tall; pipe stubs face -z. */
function buildTower(): Asset {
  const kit = new Kit();
  for (let index = 0; index < 4; index += 1) {
    const angle = Math.PI / 4 + (index * Math.PI) / 2;
    kit.add('concrete', box(0.5, 0.4, 0.5, 0.03), { at: [Math.cos(angle) * 1.15, 0.2, Math.sin(angle) * 1.15] });
  }
  kit.add('steel', torus(1.15, 0.05, 40), { at: [0, 0.42, 0] });
  // Basin with a rolled lip.
  kit.add('brand', lathe([[0, 0.4], [1.58, 0.4], [1.66, 0.43], [1.75, 0.9], [1.82, 0.94], [1.82, 0.99], [1.7, 0.99], [1.6, 0.98]], 64));
  // Air inlet: dark fill behind horizontal louvre rings and posts.
  kit.add('rubber', drum(1.62, 0.66, 0.01, 48), { at: [0, 0.97, 0] });
  for (let index = 0; index < 5; index += 1) {
    const y = 1.03 + index * 0.115;
    kit.add('panel', lathe([[1.7, y], [1.79, y + 0.07], [1.79, y + 0.085], [1.7, y + 0.015], [1.7, y]], 64));
  }
  for (let index = 0; index < 16; index += 1) {
    const angle = (index / 16) * Math.PI * 2;
    kit.add('paint', box(0.06, 0.66, 0.05, 0.015), { at: [Math.cos(angle) * 1.79, 1.3, Math.sin(angle) * 1.79], rotate: [0, -angle, 0] });
  }
  // Casing curving in to the fan deck, with moulded ribs.
  kit.add('panel', lathe([[1.6, 1.62], ...TOWER_BODY, [1.3, 3.04], [0, 3.04]], 72));
  for (let index = 0; index < 16; index += 1) {
    const angle = (index / 16) * Math.PI * 2 + Math.PI / 16;
    const points = TOWER_BODY.map(([r, y]) => [Math.cos(angle) * (r + 0.012), y, Math.sin(angle) * (r + 0.012)] as V3);
    kit.add('paint', tube(points, 0.022, 6, 0.2));
  }
  // Fan stack: a shroud with wall thickness and a rolled rim.
  kit.add('brand', lathe([[1.32, 3.0], [1.32, 3.08], [1.17, 3.1], [1.17, 3.62], [1.21, 3.65], [1.21, 3.71], [1.13, 3.71], [1.13, 3.12], [0, 3.12]], 64));
  // Guard over the fan.
  kit.add('steel', torus(1.15, 0.018, 64), { at: [0, 3.73, 0] });
  for (const radius of [0.42, 0.8]) kit.add('steel', torus(radius, 0.01, 48), { at: [0, 3.73, 0] });
  for (let index = 0; index < 8; index += 1) kit.add('steel', box(2.3, 0.018, 0.018, 0), { at: [0, 3.73, 0], rotate: [0, (index / 8) * Math.PI, 0] });
  kit.add('darkSteel', box(2.28, 0.09, 0.1, 0.01), { at: [0, 3.2, 0] });
  kit.add('paintDark', drum(0.17, 0.2, 0.02, 24), { at: [0, 3.14, 0] });
  // Ladder up the casing (+x side, toward the viewer).
  const ladderAngle = Math.PI / 4;
  const out = (r: number, y: number, t: number): V3 => [Math.cos(ladderAngle) * r - Math.sin(ladderAngle) * t, y, Math.sin(ladderAngle) * r + Math.cos(ladderAngle) * t];
  for (const t of [-0.2, 0.2]) kit.add('steel', tube([out(1.98, 0.42, t), out(1.98, 1.62, t), out(1.5, 3.74, t)], 0.018, 8, 0.15));
  for (let index = 0; index < 11; index += 1) {
    const y = 0.7 + index * 0.29;
    const r = y < 1.62 ? 1.98 : 1.98 - ((y - 1.62) / 2.12) * 0.48;
    kit.add('steel', tube([out(r, y, -0.2), out(r, y, 0.2)], 0.012, 6, 0));
  }
  // Hot water in at the casing, cold water out of the basin.
  kit.add('paint', drumZ(0.1, 0.48, 0.01, 24), { at: [0, 2.2, -1.72] });
  addFlange(kit, 'paint', 0.16, { at: [0, 2.2, -1.97], rotate: TO_NEG_Z });
  kit.add('paint', drumZ(0.1, 0.42, 0.01, 24), { at: [0, 0.64, -1.84] });
  addFlange(kit, 'paint', 0.16, { at: [0, 0.64, -2.06], rotate: TO_NEG_Z });
  kit.add('steel', tube([[0.5, 0.75, -1.72], [0.5, 0.75, -2.0], [0.5, 0.2, -2.0]], 0.025, 8, 0.06));
  kit.ports.waterIn = [0, 2.2, -2.0];
  kit.ports.waterOut = [0, 0.64, -2.09];
  return kit.build();
}

/** The tower fan: hub and six twisted blades, turning about y; sits at y = 3.32 in the tower. */
function buildTowerFan(): Asset {
  const kit = new Kit();
  kit.add('paintDark', drum(0.17, 0.14, 0.03, 28), { at: [0, -0.07, 0] });
  kit.add('paintDark', dome(0.12, 0.08, 24), { at: [0, 0.07, 0] });
  for (let index = 0; index < 6; index += 1) {
    const blade = box(0.92, 0.022, 0.2, 0.008, 1);
    blade.rotateX(0.32);
    blade.translate(0.6, 0, 0);
    kit.add('paintDark', blade, { rotate: [0, (index / 6) * Math.PI * 2, 0] });
  }
  return kit.build();
}

/** Air handling unit, 3.4 × 1.5 × 1.7 m: return, filter, coil, fan and supply sections. */
function buildAhu(): Asset {
  const kit = new Kit();
  for (const z of [-0.66, 0.66]) kit.add('paintDark', box(3.4, 0.12, 0.1, 0.01), { at: [0, 0.06, z] });
  kit.add('rubber', box(3.36, 1.5, 1.42, 0.01), { at: [0, 0.89, 0] });
  const sections = [0.7, 0.5, 0.7, 1.0, 0.5];
  let x = -1.7;
  sections.forEach((length, index) => {
    const centre = x + length / 2;
    kit.add('panel', box(length - 0.014, 1.58, 1.5, 0.03), { at: [centre, 0.91, 0] });
    if (index > 0 && index < 4) {
      kit.add('brand', box(length - 0.16, 1.3, 0.022, 0.008), { at: [centre, 0.92, 0.761] });
      for (const y of [0.62, 1.22]) kit.add('steel', box(0.03, 0.1, 0.03, 0.008), { at: [centre + length / 2 - 0.15, y, 0.785] });
      for (const y of [0.42, 1.42]) kit.add('darkSteel', drum(0.014, 0.08, 0.004, 8), { at: [centre - length / 2 + 0.1, y - 0.04, 0.775] });
    }
    if (index === 3) {
      kit.add('steel', ring(0.16, 0.13, 0.02, 32), { at: [centre - 0.1, 1.2, 0.775], rotate: [HALF_PI, 0, 0] });
      kit.add('screen', new THREE.CircleGeometry(0.13, 32), { at: [centre - 0.1, 1.2, 0.78] });
    }
    if (index === 2) {
      ['chwIn', 'chwOut'].forEach((port, portIndex) => {
        const y = portIndex === 0 ? 0.45 : 0.85;
        kit.add('paint', drumZ(0.045, 0.3, 0.008, 16), { at: [centre + 0.15, y, 0.9] });
        addValve(kit, 0.045, { at: [centre + 0.15, y, 0.96], rotate: [HALF_PI, 0, 0] });
        kit.ports[port] = [centre + 0.15, y, 1.08];
      });
      kit.add('steel', tube([[centre - 0.2, 0.18, 0.76], [centre - 0.2, 0.18, 0.95], [centre - 0.2, 0.0, 0.95]], 0.016, 8, 0.05));
    }
    x += length;
  });
  // Supply collar on top of the last section, return collar at the -x end.
  kit.add('galvanized', box(0.42, 0.18, 1.0, 0.01), { at: [1.45, 1.79, 0] });
  kit.add('galvanized', box(0.5, 0.03, 1.08, 0.006), { at: [1.45, 1.7, 0] });
  kit.add('galvanized', box(0.16, 1.0, 1.1, 0.01), { at: [-1.78, 0.95, 0] });
  kit.add('paintDark', box(0.3, 0.38, 0.12, 0.015), { at: [-1.35, 1.1, 0.81] });
  kit.ports.supply = [1.45, 1.88, 0];
  kit.ports.return = [-1.86, 0.95, 0];
  kit.ports.led = [-1.35, 1.22, 0.872];
  return kit.build();
}

/** Concealed fan coil hung under the slab: 1.2 × 0.55 m, 0.28 m deep; supply spigot at +x. */
function buildFcu(): Asset {
  const kit = new Kit();
  kit.add('galvanized', box(0.96, 0.26, 0.55, 0.012), { at: [0, 0.15, 0] });
  kit.add('grille', box(0.2, 0.24, 0.53, 0.01), { at: [-0.58, 0.15, 0] });
  kit.add('galvanized', box(0.14, 0.2, 0.46, 0.01), { at: [0.55, 0.15, 0] });
  kit.add('steel', box(1.0, 0.035, 0.6, 0.008), { at: [0, 0.02, 0] });
  for (const y of [0.1, 0.2]) kit.add('steel', tube([[0.3, y, 0.28], [0.3, y, 0.42]], 0.016, 8, 0));
  kit.add('paintDark', box(0.16, 0.1, 0.06, 0.01), { at: [-0.25, 0.18, 0.3] });
  for (const [x, z] of [[-0.42, -0.24], [-0.42, 0.24], [0.42, -0.24], [0.42, 0.24]]) {
    kit.add('steel', new THREE.CylinderGeometry(0.008, 0.008, 0.7, 6), { at: [x, 0.62, z] });
  }
  kit.ports.supply = [0.62, 0.15, 0];
  kit.ports.chw = [0.3, 0.15, 0.42];
  kit.ports.led = [-0.25, 0.18, 0.332];
  return kit.build();
}

/** VAV terminal on a supply branch: casing, round inlet, actuator and controller. */
function buildVav(): Asset {
  const kit = new Kit();
  kit.add('galvanized', box(0.8, 0.32, 0.42, 0.012), { at: [0, 0.16, 0] });
  kit.add('galvanized', drumX(0.13, 0.28, 0.01, 24), { at: [-0.52, 0.16, 0] });
  kit.add('steel', ring(0.15, 0.13, 0.03, 24), { at: [-0.5, 0.16, 0], rotate: TO_X });
  kit.add('paintDark', box(0.12, 0.09, 0.1, 0.012), { at: [-0.3, 0.37, 0] });
  kit.add('paintDark', box(0.26, 0.2, 0.05, 0.012), { at: [0.1, 0.17, 0.24] });
  for (const [x, z] of [[-0.3, -0.18], [-0.3, 0.18], [0.3, -0.18], [0.3, 0.18]]) {
    kit.add('steel', new THREE.CylinderGeometry(0.007, 0.007, 0.7, 6), { at: [x, 0.66, z] });
  }
  kit.ports.led = [0.18, 0.22, 0.266];
  return kit.build();
}

/** Four-way square ceiling diffuser with its neck, face at y = 0 looking down. */
function buildDiffuser(): Asset {
  const kit = new Kit();
  kit.add('galvanized', box(0.3, 0.24, 0.3, 0.01), { at: [0, 0.14, 0] });
  kit.add('panel', box(0.6, 0.03, 0.6, 0.008), { at: [0, 0.015, 0] });
  kit.add('grille', box(0.5, 0.01, 0.5, 0.004), { at: [0, -0.002, 0] });
  for (const [size, y] of [[0.44, -0.012], [0.32, -0.024], [0.2, -0.036]] as const) kit.add('panel', box(size, 0.014, size, 0.005), { at: [0, y, 0] });
  return kit.build();
}

/** A linear return grille, 1.2 m, face down. */
function buildReturnGrille(): Asset {
  const kit = new Kit();
  kit.add('panel', box(1.2, 0.03, 0.3, 0.006), { at: [0, 0.015, 0] });
  for (let index = 0; index < 9; index += 1) kit.add('grille', box(1.12, 0.012, 0.018, 0.003), { at: [0, -0.002, -0.12 + index * 0.03] });
  return kit.build();
}

/** A rooftop exhaust fan: curb, hood and cowl. */
function buildExhaustFan(): Asset {
  const kit = new Kit();
  kit.add('galvanized', box(0.9, 0.3, 0.9, 0.02), { at: [0, 0.15, 0] });
  kit.add('panel', drum(0.36, 0.3, 0.04, 36), { at: [0, 0.3, 0] });
  kit.add('panel', dome(0.5, 0.26, 40), { at: [0, 0.62, 0] });
  kit.add('grille', drum(0.34, 0.06, 0.01, 36), { at: [0, 0.58, 0] });
  return kit.build();
}

const builders = {
  chiller: buildChiller,
  pump: buildPump,
  coupling: buildCoupling,
  tower: buildTower,
  towerFan: buildTowerFan,
  ahu: buildAhu,
  fcu: buildFcu,
  vav: buildVav,
  diffuser: buildDiffuser,
  returnGrille: buildReturnGrille,
  exhaustFan: buildExhaustFan,
} satisfies Record<string, () => Asset>;

export type EquipmentAsset = keyof typeof builders;

const cache = new Map<EquipmentAsset, Asset>();

export function equipmentAsset(name: EquipmentAsset): Asset {
  const cached = cache.get(name);
  if (cached) return cached;
  const asset = builders[name]();
  cache.set(name, asset);
  return asset;
}
