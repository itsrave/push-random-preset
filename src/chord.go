package main

// chord.go — Shift + <button> chord detection. Two chords, each firing a random
// load and a green LED flash on its own button:
//   Shift + Add  (CC49+CC32) -> random preset
//   Shift + Swap (CC49+CC33) -> random drum rack
// State machine mirrors keyboard-visualizer/push-manager: track held buttons,
// fire when Shift + the trigger are both down, debounced 500ms per trigger.

import (
	"sync"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsaseq"
	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

const (
	ccShift  = uint8(push3.CCShift)  // 49
	ccSelect = uint8(push3.CCSelect) // 48 — adds the "open menu" variant
	ccAdd    = uint8(push3.CCAdd)    // 32
	ccSwap   = uint8(push3.CCSwap)   // 33

	chordDebounce = 500 * time.Millisecond
)

// chordDef: Shift + trigger fires a load of `kind`.
type chordDef struct {
	trigger uint8
	kind    string // "preset" | "drumrack"
}

var chords = []chordDef{
	{trigger: ccAdd, kind: "preset"},
	{trigger: ccSwap, kind: "drumrack"},
}

// triggers is the set of CCs the chord watcher cares about: Shift + Select (the
// two modifiers) + every trigger, plus the jog-wheel CCs the menu navigates by.
// midi.go only feeds these through.
var triggers = func() map[uint8]bool {
	m := map[uint8]bool{
		ccShift: true, ccSelect: true,
		push3.CCJogWheel: true, push3.CCJogPress: true,
		push3.CCJogClickLeft: true, push3.CCJogClickRight: true,
	}
	for _, c := range chords {
		m[c.trigger] = true
	}
	return m
}()

var (
	chordMu       sync.Mutex
	chordHeld     = map[uint8]bool{}
	chordLastFire = map[uint8]time.Time{} // per trigger
)

// firedChord is one decision from firedChords: which kind, and whether Select
// was also held (open the category menu instead of loading).
type firedChord struct {
	kind string
	menu bool
}

// onChordCC updates held-state for cc and acts on any chord this press
// completes, each off the MIDI thread (display/load HTTP must not block the read
// loop). While the menu is open it owns all input.
func onChordCC(cc, val byte, pmURL string) {
	if activeMenu.handle(cc, val) {
		return
	}
	for _, c := range firedChords(cc, val, time.Now()) {
		if c.menu {
			go openCategoryMenu(pmURL, c.kind)
		} else {
			go loadRandom(pmURL, c.kind)
		}
	}
}

// firedChords is the pure decision: update held state for cc, then act on a
// chord this very PRESS completes. Shift + trigger loads a random preset; adding
// Select (Shift + Select + trigger) opens that kind's category menu instead —
// Select held is what distinguishes the two, so they never both fire. A press
// "completes" a combo when cc is one of its members and the others are already
// held (either order). Releases never fire; debounced per trigger. Split out so
// it's unit-testable without ALSA or the network.
func firedChords(cc, val byte, now time.Time) []firedChord {
	chordMu.Lock()
	defer chordMu.Unlock()
	if val > 0 {
		chordHeld[cc] = true
	} else {
		delete(chordHeld, cc)
		return nil
	}
	var out []firedChord
	for _, c := range chords {
		menuReady := chordHeld[ccShift] && chordHeld[ccSelect] && chordHeld[c.trigger]
		loadReady := chordHeld[ccShift] && chordHeld[c.trigger] && !chordHeld[ccSelect]
		involved := cc == c.trigger || cc == ccShift || cc == ccSelect
		if !involved || now.Sub(chordLastFire[c.trigger]) < chordDebounce {
			continue
		}
		switch {
		case menuReady:
			chordLastFire[c.trigger] = now
			out = append(out, firedChord{kind: c.kind, menu: true})
		case loadReady:
			chordLastFire[c.trigger] = now
			out = append(out, firedChord{kind: c.kind})
		}
	}
	return out
}

// resetChordState clears held/debounce tracking. Called on menu open/close: while
// the menu swallows input, release events never reach the tracker, so its held
// map would otherwise keep the entry chord's keys "down" forever.
func resetChordState() {
	chordMu.Lock()
	chordHeld = map[uint8]bool{}
	chordLastFire = map[uint8]time.Time{}
	chordMu.Unlock()
}

// detectPush3Port finds "Ableton Push 3 Live Port" by name (requireCaps=0 —
// we only watch a few CCs, never do a capability-filtered read).
func detectPush3Port() (client, port byte, ok bool) {
	p, found := alsaseq.FindByName("Ableton Push 3 Live Port", 0)
	if !found {
		return 0, 0, false
	}
	return p.Addr.Client, p.Addr.Port, true
}
