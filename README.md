# Random Preset

Two Push 3 chords that drop something random onto the selected track:

- **Shift + Add** (CC49+CC32) → a random **preset** (prefers Instruments)
- **Shift + Swap** (CC49+CC33) → a random **drum rack kit**

Add **Select** to either chord to first pick a category:

- **Shift + Select + Add** → on-screen menu of instrument categories
- **Shift + Select + Swap** → on-screen menu of drum categories

The menu is drawn on Push's display (like push-manager's catalogue) and driven
by the **jog wheel**: turn to scroll, click-right or press to pick, click-left
to cancel. Categories are push-manager's per-category `device` facet (from
`/api/presets/facets` — instrument devices / drum-rack families; not Live's
semantic "Sounds" tags, which push-manager doesn't index). Picking one loads a
random preset from it *and* makes it sticky, so the plain Shift+Add / Shift+Swap
chords keep pulling from that category until you change it. The top **Any** entry
clears the filter. The menu auto-dismisses after 15s.

A hack for [`push-hack`](https://github.com/federico-pepe/ableton-push-hack)
(Push 3's on-device hack framework) — install it via that repo's `push-store`
hack, or build and deploy it yourself, see "Build & deploy" below.

## Requirements

**Needs the `push-hack` framework's `push-manager` and `browser-bridge` hacks
also installed and running** (from
[federico-pepe/ableton-push-hack](https://github.com/federico-pepe/ableton-push-hack)).
This hack never loads presets itself: it reads push-manager's preset index and
asks it to load onto the selected track, which push-manager forwards to
browser-bridge (the only thing that can instantiate a preset in Live). The
**PushHackBrowser** remote script must be activated in Live (browser-bridge's
one-time setup). If a load silently no-ops, that's the first thing to check —
the reason is logged to `/data/push-hack/logs/random-preset.log`.

## How it works

1. Subscribes (read-only) to Push 3's ALSA port — like keyboard-visualizer,
   an ALSA port can receive from multiple senders, so this never disturbs
   push-manager's own MIDI subscription.
2. On a chord (debounced 500ms per button; a press "completes the pair" in
   either order), GETs `push-manager /api/presets` with the matching filter,
   picks one at random.
3. POSTs `push-manager /api/live/load`. For drum racks it filters the Drums
   results down to full kits, dropping single-sound racks (individual hits
   under `Drum Hits`/`Drum Cell` folders), so Shift+Swap always loads a real
   Drum Rack, never a lone drum sound.

Runs independent of MIDI intercept — it only watches CC32/33/49, never the pad
grid's Note On/Off, so the pads keep playing into Live normally.

## Build & deploy

**Via Push Store (recommended):** install `push-hack`'s `push-manager` +
`browser-bridge` + `push-store` hacks first, then install this one from
`push-store`'s web UI (or the on-device STORE tab) — it's listed in
[ableton-push-hack's catalogue](https://github.com/federico-pepe/ableton-push-hack/blob/main/catalogue/catalog.json).

**Manually**, from a clone of this repo:
```bash
make            # build for Push 3 (linux/amd64); resolves core v0.1.0 from GitHub
```
This produces `build/random-preset` (and copies it to the repo root). Deploy it
to `/data/push-hack/hacks/random-preset/` alongside this repo's `hack.json`,
and register/start it the same way the framework's own `install.sh` does
(create `/etc/init.d/push-hack-random-preset`, `start-stop-daemon` it).

Run the tests (linux target — the package imports `core/alsaseq`, linux-only):
```bash
cd src && GOOS=linux GOARCH=amd64 go test ./...
```

**Releasing a new version:** bump `hack.json`'s `version`, then
`git tag vX.Y.Z && git push origin vX.Y.Z` — `.github/workflows/release.yml`
builds, publishes the release tarball, and updates `release.json` so
push-store's next install/update picks it up automatically, no action needed
in the main repo's catalogue.
