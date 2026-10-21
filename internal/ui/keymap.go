package ui

import (
	"sort"
	"strings"
)

// Close to forty bindings and no way to change any of them. A keymap is a
// rename applied before the switch rather than a rewrite of it: the config
// names an action and the key that should trigger it, the key handler still
// sees the key it has always seen, and the help overlay renders what is
// actually bound rather than what shipped.

// defaultKeys maps each rebindable action to the key it has always used. The
// action names are what appear in the config file, so they are deliberately
// descriptive rather than terse.
var defaultKeys = map[string]string{
	"help":            "?",
	"settings":        ",",
	"views":           "V",
	"quit":            "q",
	"search":          "/",
	"search-next":     "n",
	"search-prev":     "N",
	"details":         "enter",
	"copy":            "y",
	"export":          "e",
	"signal":          "x",
	"follow":          "F",
	"sort":            "s",
	"sort-reverse":    "S",
	"totals":          "t",
	"rate-unit":       "U",
	"aggregate":       "A",
	"tree":            "T",
	"hide-noise":      "h",
	"external-ip":     "ctrl+e",
	"pause":           " ",
	"refresh":         "R",
	"filter-proto":    "p",
	"filter-ipver":    "v",
	"filter-state":    "f",
	"filter-public":   "o",
	"filter-mine":     "u",
	"filter-iface":    "i",
	"reverse-dns":     "r",
	"group":           "g",
	"closed":          "z",
	"watch":           "w",
	"baseline-save":   "b",
	"baseline-clear":  "B",
	"baseline-accept": "a",
	"events-level":    "l",
	"events-ack":      "d",
	"events-clear":    "c",
	"search-mode":     "\\",
}

// reservedKeys can never be rebound: losing them would leave a session with no
// way out and no way to navigate.
var reservedKeys = map[string]bool{
	"ctrl+c": true, "tab": true, "shift+tab": true,
	"up": true, "down": true, "left": true, "right": true,
	"pgup": true, "pgdown": true, "home": true, "end": true,
	"esc": true, "ctrl+u": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true,
}

// keymap translates a pressed key into the key the handler expects.
type keymap struct {
	// toDefault maps a bound key onto the default key for its action, which is
	// what the big switch is written against.
	toDefault map[string]string
	// custom is the user's overrides, action -> key, kept so they can be
	// written back out unchanged.
	custom map[string]string
}

// newKeymap builds a keymap from the config's action -> key overrides. Unknown
// actions, reserved keys and duplicates are dropped: a config that would make
// the tool unusable is ignored rather than obeyed.
func newKeymap(overrides map[string]string) keymap {
	km := keymap{toDefault: map[string]string{}, custom: map[string]string{}}

	// Actions whose default key the user has taken for something else must not
	// also keep their old key, or one press would do two things.
	//
	// The actions are walked in name order rather than in map order: two
	// actions bound to the same key are resolved first-wins, and over a Go
	// map that would hand the same config file a different keyboard on every
	// start.
	taken := map[string]bool{}
	actions := make([]string, 0, len(overrides))
	for action := range overrides {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		key := strings.TrimSpace(overrides[action])
		if key == "" || reservedKeys[key] {
			continue
		}
		if _, known := defaultKeys[action]; !known {
			continue
		}
		if taken[key] {
			continue // first one wins, deterministically enough for a config
		}
		taken[key] = true
		km.custom[action] = key
	}

	// Defaults first, custom bindings second, so a custom key that is also
	// another action's default always wins. One pass in map order let the two
	// writes race, and {"copy": "x"} fired copy or signal from build to build.
	// A rebound action's own default is never entered, so that key no longer
	// fires it.
	for action, def := range defaultKeys {
		if _, ok := km.custom[action]; !ok {
			km.toDefault[def] = def
		}
	}
	for action, key := range km.custom {
		km.toDefault[key] = defaultKeys[action]
	}
	return km
}

// translate maps a pressed key to the key the handler is written against.
// Anything not bound passes through unchanged, so navigation and text input
// are untouched.
func (k keymap) translate(key string) string {
	if k.toDefault == nil {
		return key
	}
	if def, ok := k.toDefault[key]; ok {
		return def
	}
	// A key that is a default but has been given away no longer does anything.
	if _, isDefault := keyToAction[key]; isDefault {
		if _, stillBound := k.toDefault[key]; !stillBound {
			return "\x00unbound"
		}
	}
	return key
}

// keyFor is the key currently bound to an action, for the help overlay.
func (k keymap) keyFor(action string) string {
	if key, ok := k.custom[action]; ok {
		return key
	}
	return defaultKeys[action]
}

// overrides returns the user's bindings for persistence, nil when there are
// none so the config file stays clean.
func (k keymap) overrides() map[string]string {
	if len(k.custom) == 0 {
		return nil
	}
	return k.custom
}

// Actions lists the rebindable action names, for documentation and errors.
func Actions() []string {
	out := make([]string, 0, len(defaultKeys))
	for a := range defaultKeys {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// keyToAction is the reverse of defaultKeys, used to recognise a default key
// that has been reassigned.
var keyToAction = func() map[string]string {
	out := make(map[string]string, len(defaultKeys))
	for action, key := range defaultKeys {
		out[key] = action
	}
	return out
}()

// spaceLabel renders a binding for the help overlay, naming the space bar
// rather than printing an invisible key.
func spaceLabel(key string) string {
	if key == " " {
		return "space"
	}
	return key
}
