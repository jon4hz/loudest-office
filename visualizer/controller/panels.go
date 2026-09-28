package controller

import (
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// Panel kinds. A serial panel is an ESP behind a tty or tcp://host:port, a
// virtual one is mirrored over the websocket, the terminal is the local
// one: implicit, never stored.
const (
	KindSerial   = "serial"
	KindVirtual  = "virtual"
	KindTerminal = "terminal"
)

// terminalName is the implicit terminal panel's name, reserved.
const terminalName = "terminal"

// A new serial panel's defaults: checkPanel fills in the baud, the API
// pre-fills a PUT with both.
const (
	DefaultBaud       = 921600
	DefaultBrightness = 64
)

const (
	maxPanels = 16
	maxSide   = 1024 // px, a virtual panel's size each way
)

// dialRetry is the pause between two attempts to open a serial panel; a var
// so that tests shorten it.
var dialRetry = 5 * time.Second

// PanelSpec is a declared panel, as the state file and PUT /panels store it.
type PanelSpec struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`              // KindSerial | KindVirtual; KindTerminal only in PanelState
	Address        string `json:"address,omitempty"` // serial: tty path or tcp://host:port
	Baud           int    `json:"baud,omitempty"`    // serial, tty only
	W              int    `json:"w,omitempty"`       // virtual; a serial panel reports INFO's, the terminal the window's
	H              int    `json:"h,omitempty"`
	FPS            int    `json:"fps"`
	Brightness     uint8  `json:"brightness,omitempty"`      // serial
	IdleBrightness uint8  `json:"idle_brightness,omitempty"` // serial
}

// PanelState is one panel as GET /panels and GET /state report it.
type PanelState struct {
	PanelSpec
	Connected bool             `json:"connected"`        // serial: the port is open; virtual and terminal: always
	Clients   int              `json:"clients"`          // virtual: websocket clients
	Status    *proto.StatusMsg `json:"status,omitempty"` // serial, while connected
}

// Port is the part of serial.Port the controller uses.
type Port interface {
	Send([][]color.RGBA)
	SetBrightness(byte)
	Status() proto.StatusMsg
	Close()
}

// panel is a declared panel or the terminal at run time.
type panel struct {
	PanelSpec
	port       Port          // serial: nil while connecting
	quit       chan struct{} // serial: closed on delete or replace, ends the dial loop
	bright     byte          // last brightness sent
	brightSent bool
	w, h       int // effective size: the spec's, INFO's or the window's; 0 = unknown, in no group
}

// portUp says a dial loop succeeded; quit identifies the panel it was for.
type portUp struct {
	quit chan struct{}
	port Port
	info proto.InfoMsg
}

// key is the panel's render group, false while its size is unknown.
func (p *panel) key() (groupKey, bool) {
	if p.w <= 0 || p.h <= 0 {
		return groupKey{}, false
	}
	return groupKey{p.w, p.h, p.FPS}, true
}

// checkPanel validates and normalises a spec: name rules, kind, fps, the
// size of a virtual panel and the address of a serial one. Fields that do
// not apply to the kind are zeroed rather than refused, so a form or a
// patch may carry them; missing serial defaults are filled in.
func checkPanel(s PanelSpec) (PanelSpec, error) {
	name, err := checkName(s.Name)
	if err != nil {
		return s, err
	}
	s.Name = name
	if s.Name == terminalName {
		return s, fmt.Errorf("%q is the terminal", terminalName)
	}
	if s.FPS < 1 || s.FPS > 120 {
		return s, fmt.Errorf("fps must be 1..120, got %d", s.FPS)
	}
	switch s.Kind {
	case KindSerial:
		if s.Address = strings.TrimSpace(s.Address); s.Address == "" {
			return s, fmt.Errorf("a serial panel needs an address, a tty or tcp://host:port")
		}
		if s.Baud == 0 {
			s.Baud = DefaultBaud
		}
		if s.Baud < 0 {
			return s, fmt.Errorf("baud must be > 0, got %d", s.Baud)
		}
		s.W, s.H = 0, 0
	case KindVirtual:
		if s.W < 1 || s.W > maxSide || s.H < 1 || s.H > maxSide {
			return s, fmt.Errorf("size must be 1..%d each way, got %dx%d", maxSide, s.W, s.H)
		}
		s.Address, s.Baud, s.Brightness, s.IdleBrightness = "", 0, 0, 0
	default:
		return s, fmt.Errorf("unknown kind %q, have: %s, %s", s.Kind, KindSerial, KindVirtual)
	}
	return s, nil
}

// specs is the declared panels, never the terminal, never a nil slice.
func (c *Controller) specs() []PanelSpec {
	out := make([]PanelSpec, 0, len(c.panels))
	for _, p := range c.panels {
		out = append(out, p.PanelSpec)
	}
	return out
}

// allPanels is every panel, the terminal last.
func (c *Controller) allPanels() []*panel {
	if c.term == nil {
		return c.panels
	}
	return append(slices.Clone(c.panels), c.term)
}

// find is the panel called name, the terminal included; nil when none.
func (c *Controller) find(name string) *panel {
	for _, p := range c.allPanels() {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// state reports p.
func (c *Controller) state(p *panel) PanelState {
	ps := PanelState{PanelSpec: p.PanelSpec, Connected: true}
	ps.W, ps.H = p.w, p.h
	switch p.Kind {
	case KindSerial:
		ps.Connected = p.port != nil
		if p.port != nil {
			st := p.port.Status()
			ps.Status = &st
		}
	case KindVirtual:
		if c.o.Clients != nil {
			ps.Clients = c.o.Clients(p.Name)
		}
	}
	return ps
}

// Panels lists every panel in declaration order, the terminal last.
func (c *Controller) Panels() []PanelState {
	all := c.allPanels()
	out := make([]PanelState, len(all))
	for i, p := range all {
		out[i] = c.state(p)
	}
	return out
}

// FirstPanel is the first panel's name, the terminal's without declared
// ones, "" without any: what GET /frames mirrors.
func (c *Controller) FirstPanel() string {
	if all := c.allPanels(); len(all) > 0 {
		return all[0].Name
	}
	return ""
}

// HasPanel reports whether a panel called name exists, the terminal included.
func (c *Controller) HasPanel(name string) bool { return c.find(name) != nil }

// PutPanel declares a panel, replacing one of the same name, and saves. A
// serial panel whose address and baud did not change keeps its port; any
// other change to a serial panel closes it and dials again.
func (c *Controller) PutPanel(spec PanelSpec) (PanelState, error) {
	spec, err := checkPanel(spec)
	if err != nil {
		return PanelState{}, err
	}
	old := c.find(spec.Name)
	if old == nil && len(c.panels) >= maxPanels {
		return PanelState{}, fmt.Errorf("too many panels, max %d", maxPanels)
	}
	var p *panel
	switch {
	case old != nil && old.Kind == KindSerial && spec.Kind == KindSerial && old.Address == spec.Address && old.Baud == spec.Baud:
		c.apply(old, spec)
		p = old
	case old != nil && old.Kind == KindVirtual && spec.Kind == KindVirtual:
		c.apply(old, spec)
		p = old
	default:
		if old != nil {
			c.removePanel(old)
		}
		p, err = c.addPanel(spec)
		if err != nil {
			return PanelState{}, err
		}
	}
	if err := c.save(); err != nil {
		return c.state(p), fmt.Errorf("%w: %v", ErrSave, err)
	}
	return c.state(p), nil
}

// PatchPanel merges raw onto the panel's spec: fps, brightness,
// idle_brightness, and w, h for a virtual panel. Name, kind, address and
// baud cannot change through a patch, PUT does that.
func (c *Controller) PatchPanel(name string, raw json.RawMessage) (PanelState, error) {
	p := c.find(name)
	if p == nil {
		return PanelState{}, fmt.Errorf("unknown panel %q: %w", name, ErrNotFound)
	}
	if p.Kind == KindTerminal {
		return PanelState{}, fmt.Errorf("the terminal is not editable")
	}
	spec, err := bubble.Patch(p.PanelSpec, raw)
	if err != nil {
		return PanelState{}, err
	}
	if spec, err = checkPanel(spec); err != nil {
		return PanelState{}, err
	}
	if spec.Name != p.Name || spec.Kind != p.Kind || spec.Address != p.Address || spec.Baud != p.Baud {
		return PanelState{}, fmt.Errorf("only fps, brightness, idle_brightness, w and h can be patched")
	}
	c.apply(p, spec)
	if err := c.save(); err != nil {
		return c.state(p), fmt.Errorf("%w: %v", ErrSave, err)
	}
	return c.state(p), nil
}

// DeletePanel removes a declared panel, closing its port, and saves.
func (c *Controller) DeletePanel(name string) error {
	p := c.find(name)
	if p == nil {
		return fmt.Errorf("unknown panel %q: %w", name, ErrNotFound)
	}
	if p.Kind == KindTerminal {
		return fmt.Errorf("the terminal cannot be deleted")
	}
	c.removePanel(p)
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// addPanel declares a checked spec without saving: a virtual panel joins
// its group at once, a serial one starts dialing.
func (c *Controller) addPanel(spec PanelSpec) (*panel, error) {
	if c.find(spec.Name) != nil {
		return nil, fmt.Errorf("duplicate panel %q", spec.Name)
	}
	p := &panel{PanelSpec: spec}
	c.panels = append(c.panels, p)
	switch p.Kind {
	case KindVirtual:
		p.w, p.h = p.W, p.H
		c.join(p)
	case KindSerial:
		p.quit = make(chan struct{})
		c.pending = append(c.pending, c.dial(p))
	}
	return p, nil
}

// apply moves p to a new spec of the same identity: out of its group, the
// spec, back into the group that now fits. A change that keeps the group
// (brightness) keeps it, so the picture does not restart. The brightness is
// sent again.
func (c *Controller) apply(p *panel, spec PanelSpec) {
	w, h := p.w, p.h
	if spec.Kind == KindVirtual {
		w, h = spec.W, spec.H
	}
	old, ok := p.key()
	move := !ok || old != (groupKey{w, h, spec.FPS})
	if move {
		c.leave(p)
	}
	p.PanelSpec, p.w, p.h, p.brightSent = spec, w, h, false
	if move {
		c.join(p)
	}
}

// removePanel forgets p: its group, its dial loop and its port.
func (c *Controller) removePanel(p *panel) {
	c.leave(p)
	if p.quit != nil {
		close(p.quit)
		p.quit = nil
	}
	if p.port != nil {
		p.port.Close()
		p.port = nil
	}
	c.panels = slices.DeleteFunc(c.panels, func(q *panel) bool { return q == p })
}

// dial is the cmd that opens a serial panel: it tries until the port
// answers or the panel is removed, then reports portUp. It runs off the
// update loop, since the handshake takes seconds and a panel may be off.
func (c *Controller) dial(p *panel) tea.Cmd {
	dial := c.o.Dial
	if dial == nil {
		return nil
	}
	addr, baud, bright, quit := p.Address, p.Baud, p.Brightness, p.quit
	return func() tea.Msg {
		for {
			port, info, err := dial(addr, baud, bright)
			if err == nil {
				return portUp{quit: quit, port: port, info: info}
			}
			select {
			case <-quit:
				return nil
			case <-time.After(dialRetry):
			}
		}
	}
}

// portUp is the handler: the panel the dial was for takes the port and
// joins its group; a panel removed or replaced meanwhile closes it.
func (c *Controller) portUp(msg portUp) {
	for _, p := range c.panels {
		if p.quit == msg.quit && p.quit != nil {
			p.port, p.w, p.h = msg.port, int(msg.info.W), int(msg.info.H)
			p.brightSent = false
			c.join(p)
			return
		}
	}
	msg.port.Close()
}

// wantBright is p's brightness for the shown kind: idle and blank dim.
func (c *Controller) wantBright(p *panel) byte {
	switch c.kind {
	case bubble.Music, bubble.EventKind, bubble.Track:
		return p.Brightness
	}
	return p.IdleBrightness
}

// Close releases every serial port and stops every dial loop; for main,
// after the program ended.
func (c *Controller) Close() {
	for _, p := range c.panels {
		if p.quit != nil {
			close(p.quit)
			p.quit = nil
		}
		if p.port != nil {
			p.port.Close()
			p.port = nil
		}
	}
}
