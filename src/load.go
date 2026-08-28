package main

// load.go — pick a random preset (or drum rack) from push-manager's index and
// ask it to load onto the selected track (push-manager forwards to browser-
// bridge, the only thing that can instantiate a preset in Live).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 5 * time.Second}

type presetEntry struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Path     string `json:"path"`
}

// deviceParam is the "&device=<sticky category>" query fragment, or "" for Any.
func deviceParam(kind string) string {
	if d := getFilter(kind); d != "" {
		return "&device=" + url.QueryEscape(d)
	}
	return ""
}

// fetchCategories returns push-manager's `device` facet values for a coarse
// category (Instruments / Drums) — the browsable sub-groupings the menu offers.
func fetchCategories(pmURL, category string) ([]string, error) {
	resp, err := httpClient.Get(pmURL + "/api/presets/facets")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		DevicesByCategory map[string][]string `json:"devices_by_category"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.DevicesByCategory[category], nil
}

func fetchPresets(pmURL, query string) ([]presetEntry, error) {
	resp, err := httpClient.Get(pmURL + "/api/presets?" + query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Presets []presetEntry `json:"presets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Presets, nil
}

// realDrumRacks keeps only full Drum Rack kits, dropping single-sound racks.
// In the Push library both are .adg racks in the Drums category, but individual
// hits live under a "Drum Hits" / "Drum Cell" folder (e.g. Drums/Drum Hits/Bell/
// "Agogo A High") — excluding those leaves the actual kits ("212 Kit", etc.).
func realDrumRacks(in []presetEntry) []presetEntry {
	out := make([]presetEntry, 0, len(in))
	for _, p := range in {
		lp := strings.ToLower(p.Path)
		if strings.Contains(lp, "drum hits") || strings.Contains(lp, "drum cell") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// loadRandom loads a random preset or drum rack onto the selected track,
// honouring the sticky category filter set via the menu (getFilter).
func loadRandom(pmURL, kind string) {
	var presets []presetEntry
	var err error
	dev := deviceParam(kind)

	switch kind {
	case "drumrack":
		presets, err = fetchPresets(pmURL, "type=preset&filter=Drums&rack=rack"+dev)
		if err == nil {
			presets = realDrumRacks(presets)
		}
	default: // "preset"
		presets, err = fetchPresets(pmURL, "type=preset&filter=Instruments"+dev)
		if err == nil && len(presets) == 0 && dev == "" {
			presets, err = fetchPresets(pmURL, "type=preset") // fall back to any preset
		}
	}
	if err != nil {
		log.Printf("load: fetch %s: %v", kind, err)
		return
	}
	if len(presets) == 0 {
		log.Printf("load: no %s found (is push-manager's library scanned?)", kind)
		return
	}

	p := presets[rand.Intn(len(presets))]
	if err := livePost(pmURL, p); err != nil {
		log.Printf("load: %q: %v (is browser-bridge's PushHackBrowser script activated in Live?)", p.Name, err)
		return
	}
	log.Printf("load: %s -> %q (%s)", kind, p.Name, p.Category)
}

func livePost(pmURL string, p presetEntry) error {
	payload, _ := json.Marshal(map[string]string{"name": p.Name, "category": p.Category, "type": "preset"})
	resp, err := httpClient.Post(pmURL+"/api/live/load", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if !r.OK {
		if r.Error != "" {
			return fmt.Errorf("%s", r.Error)
		}
		return fmt.Errorf("load rejected")
	}
	return nil
}
