// The film's clock, shared by the picture and the music: twin/direction.ts imports it and the twin
// bundle hands it to index.html as HVACTwin.cues; music/compose.ts imports it too. 120 BPM: a beat
// is 0.5 s and a bar 2 s. Every cut and every hit is named here.
export const CUES = {
  bpm: 120,
  duration: 60,
  // Chapters: each opens on a bar line with its word on a colour flood.
  hook: 0,            // the cover: the plant in morning light, the promise in big type
  forecast: 3.6,      // today's load drawn across the frame; the saved band under it
  drop: 8,            // the band fills, the beat comes in
  see: 10,            // 看得清
  think: 18,          // 算得准
  night: 30,          // 靠得住
  save: 40,           // 省得下
  gather: 50.2,       // the year's tiles fly together...
  mark: 52,           // ...into the windmill
  // 看得清: the plant's live figures, the building floor by floor, where the power goes, the pipes.
  live: 10.5, floors: 12.5, flow: 15, pipes: 17,
  // 算得准: one card asks, proposes, confirms and sends; the toast flies into 1# chiller; half an hour
  // later the platform checks the change held.
  ask: 18.5, askSend: 19.3, propose: 21.5, dispatch: 23.4, confirm: 24.3, sent: 24.35, flyIn: 25.1,
  chiller: 25.5, verify: 27.5, copLands: 28.9,
  // 靠得住: 03:12, the phone, then the diagnosis it found by itself.
  clockRun: 30.5, fault: 32.5, tapAlarm: 33.4, tapDispatch: 34.3, cleared: 35.6, fdd: 36.5,
  // 省得下: yesterday on the phone, yesterday as one tile, the year as a wall of tiles.
  daily: 40.5, tapDaily: 41.3, yesterday: 42.5, wall: 43.6, figures: [45, 46.6, 48.2],
  // Sounds, each caused by something on screen (seconds).
  clicks: [19.3, 23.4, 24.3, 33.4, 34.3, 41.3],
  whooshes: [3.6, 10, 10.5, 12.5, 15, 17, 18, 18.5, 21.5, 25.1, 27.5, 30, 36.5, 40, 42.5, 43.6, 50.2],
  pops: [8.6, 24.35, 28.9, 35.6, 37.3, 45.25, 46.85, 48.45],
  impacts: [8, 52],
};
