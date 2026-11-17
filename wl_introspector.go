package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"
)

// This file is a minimal Wayland client that follows which application has
// the focus through wlr-foreign-toplevel-management-unstable-v1 (sway,
// Hyprland and other wlroots compositors). It speaks just enough of the wire
// protocol for that: wl_display, wl_registry and the two toplevel interfaces.
// A failure here only disables per-application input modes.

// wlAppId is published by the Wayland listener goroutine and read on FocusIn.
var wlAppId atomic.Value // string

func getWlAppId() string {
	appId, _ := wlAppId.Load().(string)
	return appId
}

const (
	wlToplevelManagerInterface = "zwlr_foreign_toplevel_manager_v1"
	// wlToplevelManagerVersion is the newest protocol version handled here.
	wlToplevelManagerVersion = 3

	wlDisplayId  = 1 // fixed by the protocol
	wlRegistryId = 2 // first id this client allocates
	wlManagerId  = 3

	wlDisplayGetRegistry = 1 // wl_display request
	wlDisplayError       = 0 // wl_display event
	wlRegistryBind       = 0 // wl_registry request
	wlRegistryGlobal     = 0 // wl_registry event

	wlManagerToplevel = 0 // manager event: a new toplevel handle
	wlManagerFinished = 1

	wlHandleAppId   = 1 // handle events
	wlHandleState   = 4
	wlHandleDone    = 5
	wlHandleClosed  = 6
	wlHandleDestroy = 7 // handle request

	wlToplevelStateActivated = 2

	wlHeaderSize     = 8
	wlMaxMessageSize = 1 << 16
)

// wlByteOrder is the host byte order, which is what the Wayland wire uses.
var wlByteOrder = func() binary.ByteOrder {
	var probe uint16 = 1
	if *(*byte)(unsafe.Pointer(&probe)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}()

// wlMessage builds the arguments of a request.
type wlMessage struct {
	args []byte
}

func (m *wlMessage) putUint32(v uint32) {
	var buf [4]byte
	wlByteOrder.PutUint32(buf[:], v)
	m.args = append(m.args, buf[:]...)
}

// putString writes the length including the terminating NUL, then the bytes
// padded with NULs to a multiple of four. The declared length must not
// include the padding: libwayland closes the connection otherwise.
func (m *wlMessage) putString(s string) {
	var length = len(s) + 1
	m.putUint32(uint32(length))
	var padded = make([]byte, (length+3)&^3)
	copy(padded, s)
	m.args = append(m.args, padded...)
}

func (m *wlMessage) send(w io.Writer, object uint32, opcode uint32) error {
	var header [wlHeaderSize]byte
	wlByteOrder.PutUint32(header[0:4], object)
	wlByteOrder.PutUint32(header[4:8], uint32(wlHeaderSize+len(m.args))<<16|opcode)
	_, err := w.Write(append(header[:], m.args...))
	return err
}

// wlEvent reads the arguments of an event.
type wlEvent struct {
	object uint32
	opcode uint32
	args   []byte
}

var errWlShortEvent = errors.New("wayland: truncated event")

func (e *wlEvent) uint32() (uint32, error) {
	if len(e.args) < 4 {
		return 0, errWlShortEvent
	}
	v := wlByteOrder.Uint32(e.args)
	e.args = e.args[4:]
	return v, nil
}

// bytes reads a length-prefixed, padded block (a string or an array).
func (e *wlEvent) bytes() ([]byte, error) {
	length, err := e.uint32()
	if err != nil {
		return nil, err
	}
	var padded = (int(length) + 3) &^ 3
	if padded > len(e.args) {
		return nil, errWlShortEvent
	}
	data := e.args[:length]
	e.args = e.args[padded:]
	return data, nil
}

func (e *wlEvent) string() (string, error) {
	data, err := e.bytes()
	if err != nil {
		return "", err
	}
	// Drop the terminating NUL.
	for len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	return string(data), nil
}

func readWlEvent(r io.Reader) (*wlEvent, error) {
	var header [wlHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	var sizeAndOpcode = wlByteOrder.Uint32(header[4:8])
	var size = int(sizeAndOpcode >> 16)
	if size < wlHeaderSize || size > wlMaxMessageSize {
		return nil, fmt.Errorf("wayland: invalid event size %d", size)
	}
	ev := &wlEvent{
		object: wlByteOrder.Uint32(header[0:4]),
		opcode: sizeAndOpcode & 0xffff,
		args:   make([]byte, size-wlHeaderSize),
	}
	if _, err := io.ReadFull(r, ev.args); err != nil {
		return nil, err
	}
	return ev, nil
}

// wlToplevel is what the compositor has told us about one window. Changes
// are pending until its "done" event.
type wlToplevel struct {
	appId     string
	activated bool
}

// wlFocusWatcher tracks the app id of the activated toplevel.
type wlFocusWatcher struct {
	conn      io.ReadWriter
	onChange  func(appId string)
	toplevels map[uint32]*wlToplevel
	focused   uint32 // handle id of the activated toplevel, 0 if none
	bound     bool
}

func newWlFocusWatcher(conn io.ReadWriter, onChange func(string)) *wlFocusWatcher {
	return &wlFocusWatcher{conn: conn, onChange: onChange, toplevels: map[uint32]*wlToplevel{}}
}

// run asks for the globals and handles events until the connection ends.
func (w *wlFocusWatcher) run() error {
	var getRegistry wlMessage
	getRegistry.putUint32(wlRegistryId)
	if err := getRegistry.send(w.conn, wlDisplayId, wlDisplayGetRegistry); err != nil {
		return err
	}
	for {
		ev, err := readWlEvent(w.conn)
		if err != nil {
			return err
		}
		if err := w.handle(ev); err != nil {
			return err
		}
	}
}

func (w *wlFocusWatcher) handle(ev *wlEvent) error {
	switch {
	case ev.object == wlDisplayId:
		if ev.opcode == wlDisplayError {
			return w.protocolError(ev)
		}
	case ev.object == wlRegistryId:
		if ev.opcode == wlRegistryGlobal {
			return w.handleGlobal(ev)
		}
	case ev.object == wlManagerId:
		return w.handleManager(ev)
	default:
		if toplevel, ok := w.toplevels[ev.object]; ok {
			return w.handleToplevel(ev, toplevel)
		}
	}
	return nil
}

func (w *wlFocusWatcher) protocolError(ev *wlEvent) error {
	object, _ := ev.uint32()
	code, _ := ev.uint32()
	message, _ := ev.string()
	return fmt.Errorf("wayland: protocol error on object %d (code %d): %s", object, code, message)
}

func (w *wlFocusWatcher) handleGlobal(ev *wlEvent) error {
	name, err := ev.uint32()
	if err != nil {
		return err
	}
	iface, err := ev.string()
	if err != nil {
		return err
	}
	version, err := ev.uint32()
	if err != nil {
		return err
	}
	if iface != wlToplevelManagerInterface || w.bound {
		return nil
	}
	if version > wlToplevelManagerVersion {
		version = wlToplevelManagerVersion
	}
	var bind wlMessage
	bind.putUint32(name)
	bind.putString(iface)
	bind.putUint32(version)
	bind.putUint32(wlManagerId)
	w.bound = true
	return bind.send(w.conn, wlRegistryId, wlRegistryBind)
}

func (w *wlFocusWatcher) handleManager(ev *wlEvent) error {
	switch ev.opcode {
	case wlManagerToplevel:
		handle, err := ev.uint32()
		if err != nil {
			return err
		}
		w.toplevels[handle] = &wlToplevel{}
	case wlManagerFinished:
		return io.EOF
	}
	return nil
}

func (w *wlFocusWatcher) handleToplevel(ev *wlEvent, toplevel *wlToplevel) error {
	switch ev.opcode {
	case wlHandleAppId:
		appId, err := ev.string()
		if err != nil {
			return err
		}
		toplevel.appId = appId
	case wlHandleState:
		states, err := ev.bytes()
		if err != nil {
			return err
		}
		toplevel.activated = false
		for i := 0; i+4 <= len(states); i += 4 {
			if wlByteOrder.Uint32(states[i:]) == wlToplevelStateActivated {
				toplevel.activated = true
			}
		}
	case wlHandleDone:
		w.applyToplevel(ev.object, toplevel)
	case wlHandleClosed:
		delete(w.toplevels, ev.object)
		if w.focused == ev.object {
			w.setFocused(0, "")
		}
		// Let the compositor free the handle.
		return (&wlMessage{}).send(w.conn, ev.object, wlHandleDestroy)
	}
	return nil
}

// applyToplevel publishes the state a toplevel settled on.
func (w *wlFocusWatcher) applyToplevel(handle uint32, toplevel *wlToplevel) {
	if toplevel.activated {
		w.setFocused(handle, toplevel.appId)
	} else if w.focused == handle {
		w.setFocused(0, "")
	}
}

func (w *wlFocusWatcher) setFocused(handle uint32, appId string) {
	w.focused = handle
	w.onChange(appId)
}

func wlSocketPath() (string, error) {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	if filepath.IsAbs(display) {
		return display, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtimeDir, display), nil
}

// wlGetFocusWindowClass follows the focused application until the Wayland
// connection ends. It never takes the input method down with it.
func wlGetFocusWindowClass() {
	path, err := wlSocketPath()
	if err != nil {
		log.Printf("wayland: focus tracking disabled: %s", err)
		return
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		log.Printf("wayland: focus tracking disabled: %s", err)
		return
	}
	defer conn.Close()
	watcher := newWlFocusWatcher(conn, func(appId string) {
		wlAppId.Store(appId)
		debugf("wlAppId = %s", appId)
	})
	if err := watcher.run(); err != nil && err != io.EOF {
		log.Printf("wayland: focus tracking stopped: %s", err)
	}
	wlAppId.Store("")
}
