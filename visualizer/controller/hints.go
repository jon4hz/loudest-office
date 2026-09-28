package controller

import "github.com/jon4hz/loudest-office/visualizer/bubble"

// Hints collects every registered bubble's form hints, an empty map for a
// bubble without settings, keyed by bubble name.
func (c *Controller) Hints() map[string]map[string]bubble.Hint {
	out := make(map[string]map[string]bubble.Hint, len(c.entries))
	for _, e := range c.entries {
		hints := map[string]bubble.Hint{}
		if h, ok := e.tmpl.(bubble.Hinter); ok {
			hints = h.Hints()
		}
		out[e.tmpl.Name()] = hints
	}
	return out
}
