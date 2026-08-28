package main

// menu.go — the on-screen category picker. Shift+Select+Add / Shift+Select+Swap
// open a takeover-mode list (drawn like push-manager's catalogue: black frame,
// title strip, scrollable ListView) of the categories push-manager knows for
// that kind — its per-category `device` facet (instrument / drum-rack families).
// The Push 3 jog wheel drives it: turn to scroll, click-right or press to pick,
// click-left to cancel. Picking loads a random preset from that category NOW and
// makes it sticky, so plain Shift+Add / Shift+Swap keep pulling from it until
// changed. The top "Any" entry clears the filter.
//
// We never write the display's shared memory ourselves — push-manager is the
// sole writer; we POST rendered PNGs via pmclient, same as every display hack.

import (
	"image"
	"log"
	"sync"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/gfx"
	"github.com/federico-pepe/ableton-push-hack/core/gfx/text"
	"github.com/federico-pepe/ableton-push-hack/core/gfx/widgets"
	"github.com/federico-pepe/ableton-push-hack/core/pmclient"
	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

const (
	menuRowH    = 18
	menuTopH    = 18            // title strip, mirrors ui_shadow's suiTopH
	menuVisRows = 7             // (160 - (18+13 breadcrumb)) / 18
	menuIdle    = 15 * time.Second
)

// facetCategory / title for each chord kind — kind is the same "preset"/
// "drumrack" string chord.go fires with.
func kindFacet(kind string) (category, title string) {
	if kind == "drumrack" {
		return "Drums", "RANDOM · DRUMS"
	}
	return "Instruments", "RANDOM · SOUNDS"
}

// deviceFilter is the sticky per-kind category ("" = Any). loadRandom reads it.
var (
	filterMu     sync.Mutex
	deviceFilter = map[string]string{}
)

func getFilter(kind string) string {
	filterMu.Lock()
	defer filterMu.Unlock()
	return deviceFilter[kind]
}

func setFilter(kind, dev string) {
	filterMu.Lock()
	deviceFilter[kind] = dev
	filterMu.Unlock()
}

// menuEvent is a jog action routed from the MIDI thread to the menu goroutine.
type menuEvent struct {
	move int  // cursor delta (0 for non-move)
	ok   bool // confirm
	back bool // cancel
}

// activeMenu is the single live picker (only one can be open at a time). handle()
// runs on the MIDI read thread; the goroutine in open() owns rendering + state.
var activeMenu = &menu{}

type menu struct {
	mu     sync.Mutex
	active bool
	ev     chan menuEvent
}

// isActive reports whether a picker is open (chord.go swallows all input then).
func (m *menu) isActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// handle maps a jog CC to a menu event, non-blocking so the read loop never
// stalls. Returns true if the menu consumed it (always, while active).
func (m *menu) handle(cc, val byte) bool {
	m.mu.Lock()
	ev, active := m.ev, m.active
	m.mu.Unlock()
	if !active {
		return false
	}
	var e menuEvent
	switch cc {
	case push3.CCJogWheel: // relative: turn to scroll (CW = down)
		e.move = push3.DecodeRel(val)
	case push3.CCJogClickRight, push3.CCJogPress:
		if val == 0 {
			return true
		}
		e.ok = true
	case push3.CCJogClickLeft:
		if val == 0 {
			return true
		}
		e.back = true
	default:
		return true // swallow every other button while the menu owns the screen
	}
	if e != (menuEvent{}) {
		select {
		case ev <- e:
		default: // drop if the goroutine is mid-render; jog spam is lossy-safe
		}
	}
	return true
}

// open shows the picker for kind and drives it to a decision. Runs in its own
// goroutine (chord.go spawns it); blocks that goroutine until pick/cancel/idle.
func openCategoryMenu(pmURL, kind string) {
	// Claim the single picker slot BEFORE touching the display, so a rare
	// double-open can't have one goroutine's deferred SetMode(0) tear down the
	// other's takeover.
	m := activeMenu
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return
	}
	m.active = true
	m.ev = make(chan menuEvent, 8)
	ev := m.ev
	m.mu.Unlock()
	resetChordState() // entry chord's held keys go stale while we swallow input
	defer func() {
		m.mu.Lock()
		m.active = false
		m.ev = nil
		m.mu.Unlock()
		resetChordState()
	}()

	category, title := kindFacet(kind)
	cats, err := fetchCategories(pmURL, category)
	if err != nil {
		log.Printf("menu: facets: %v", err)
		return
	}
	rows := append([]string{"Any"}, cats...) // "Any" clears the filter

	pm := pmclient.New(pmURL)
	if err := pm.SetMode(2); err != nil { // 2 = takeover
		log.Printf("menu: takeover (is push-display connected?): %v", err)
		return
	}
	defer pm.SetMode(0) // hand the screen back

	// Start the cursor on the current selection.
	cursor := 0
	for i, r := range rows {
		if r == getFilter(kind) || (i == 0 && getFilter(kind) == "") {
			cursor = i
		}
	}
	scroll := clampScroll(cursor, len(rows), 0)
	renderMenu(pm, title, kind, rows, cursor, scroll)

	for {
		select {
		case e := <-ev:
			switch {
			case e.back:
				return
			case e.ok:
				dev := rows[cursor]
				if cursor == 0 {
					dev = "" // "Any"
				}
				setFilter(kind, dev)
				go loadRandom(pmURL, kind) // load now + sticky
				return
			default:
				cursor = push3.ClampInt(cursor+e.move, 0, len(rows)-1)
				scroll = clampScroll(cursor, len(rows), scroll)
				renderMenu(pm, title, kind, rows, cursor, scroll)
			}
		case <-time.After(menuIdle):
			return // auto-dismiss so a forgotten menu never holds the screen
		}
	}
}

// clampScroll keeps cursor within the visible window [scroll, scroll+menuVisRows).
func clampScroll(cursor, total, scroll int) int {
	if cursor < scroll {
		return cursor
	}
	if cursor >= scroll+menuVisRows {
		return cursor - menuVisRows + 1
	}
	if scroll > total-menuVisRows {
		scroll = total - menuVisRows
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

// renderMenu draws one frame and POSTs it — black frame + title strip + list,
// the same visual language as push-manager's catalogue (widgets.Default theme).
func renderMenu(pm *pmclient.Client, title, kind string, rows []string, cursor, scroll int) {
	img := image.NewNRGBA(image.Rect(0, 0, push3.VisW, push3.VisH))
	t := widgets.Default

	gfx.FillRect(img, 0, 0, push3.VisW, push3.VisH, t.Black)
	gfx.FillRect(img, 0, 0, push3.VisW, menuTopH, t.DarkGray)
	text.Draw(img, 6, menuTopH-5, title, t.White)

	status := "Category: " + labelFor(getFilter(kind))
	lr := make([]widgets.ListRow, len(rows))
	for i, r := range rows {
		lr[i] = widgets.ListRow{Text: r, TextCol: t.White}
	}
	widgets.RenderList(img, t, widgets.ListView{
		Rows:       lr,
		Cursor:     cursor,
		Scroll:     scroll,
		Breadcrumb: "Turn jog to scroll · click OK · left = back",
		Status:     status,
	}, menuTopH, push3.VisW, menuRowH, push3.VisH)

	if err := pm.PushImage(img); err != nil {
		log.Printf("menu: push image: %v", err)
	}
}

func labelFor(dev string) string {
	if dev == "" {
		return "Any"
	}
	return dev
}
