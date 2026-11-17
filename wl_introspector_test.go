package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func wlEventBytes(object, opcode uint32, build func(m *wlMessage)) []byte {
	var m wlMessage
	if build != nil {
		build(&m)
	}
	var buf bytes.Buffer
	m.send(&buf, object, opcode)
	return buf.Bytes()
}

func wlStateArray(states ...uint32) func(m *wlMessage) {
	return func(m *wlMessage) {
		m.putUint32(uint32(4 * len(states)))
		for _, s := range states {
			m.putUint32(s)
		}
	}
}

func wlAppIdEvent(handle uint32, appId string) []byte {
	return wlEventBytes(handle, wlHandleAppId, func(m *wlMessage) { m.putString(appId) })
}

func TestWlStringEncoding(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantSize int // bytes after the length word
	}{
		{"empty", "", 4},
		{"length 3 mod 4", "abc", 4},
		{"length 0 mod 4", "abcd", 8},
		{"length 1 mod 4", "abcde", 8},
		{"length 2 mod 4", "abcdef", 8},
		{"toplevel manager", wlToplevelManagerInterface, 36},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m wlMessage
			m.putString(tc.in)
			if got := wlByteOrder.Uint32(m.args); got != uint32(len(tc.in)+1) {
				t.Errorf("declared length = %d, want %d (the string and its NUL, no padding)", got, len(tc.in)+1)
			}
			payload := m.args[4:]
			if len(payload) != tc.wantSize {
				t.Fatalf("payload = %d bytes, want %d", len(payload), tc.wantSize)
			}
			if nul := bytes.IndexByte(payload, 0); nul != len(tc.in) {
				t.Errorf("first NUL at offset %d, want %d", nul, len(tc.in))
			}
			ev := &wlEvent{args: m.args}
			if got, err := ev.string(); err != nil || got != tc.in || len(ev.args) != 0 {
				t.Errorf("decoded %q, err %v, %d bytes left; want %q and nothing left", got, err, len(ev.args), tc.in)
			}
		})
	}
}

func TestReadWlEventRejectsBadInput(t *testing.T) {
	cases := map[string][]byte{
		"truncated header":  {1, 0, 0},
		"size below header": wlRawHeader(1, 4, 0),
		"truncated body":    append(wlRawHeader(1, 16, 0), 1, 2),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := readWlEvent(bytes.NewReader(input)); err == nil {
				t.Errorf("want an error for %v", input)
			}
		})
	}
	ev := &wlEvent{args: []byte{9, 0, 0, 0, 'a'}}
	if wlByteOrder.Uint32(ev.args) == 9 {
		if _, err := ev.string(); err == nil {
			t.Errorf("a string longer than the event must be rejected")
		}
	}
}

func wlRawHeader(object uint32, size uint32, opcode uint32) []byte {
	var header [wlHeaderSize]byte
	wlByteOrder.PutUint32(header[0:4], object)
	wlByteOrder.PutUint32(header[4:8], size<<16|opcode)
	return header[:]
}

// fakeCompositor plays the server side of a connection.
type fakeCompositor struct {
	t    *testing.T
	conn net.Conn
}

func (c *fakeCompositor) send(messages ...[]byte) {
	c.t.Helper()
	for _, m := range messages {
		if _, err := c.conn.Write(m); err != nil {
			c.t.Fatalf("compositor write: %v", err)
		}
	}
}

func (c *fakeCompositor) expectRequest(object, opcode uint32) *wlEvent {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := readWlEvent(c.conn)
	if err != nil {
		c.t.Fatalf("compositor read: %v", err)
	}
	if req.object != object || req.opcode != opcode {
		c.t.Fatalf("request = object %d opcode %d, want object %d opcode %d", req.object, req.opcode, object, opcode)
	}
	return req
}

func startWatcher(t *testing.T) (*fakeCompositor, <-chan string, <-chan error) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	focus := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		done <- newWlFocusWatcher(client, func(appId string) { focus <- appId }).run()
	}()
	return &fakeCompositor{t: t, conn: server}, focus, done
}

func wantFocus(t *testing.T, focus <-chan string, want string) {
	t.Helper()
	select {
	case got := <-focus:
		if got != want {
			t.Fatalf("focused app id = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no focus change, want %q", want)
	}
}

func TestWlFocusWatcherFollowsTheActivatedToplevel(t *testing.T) {
	const managerName, editor, browser = 7, 0xff000001, 0xff000002
	compositor, focus, done := startWatcher(t)

	req := compositor.expectRequest(wlDisplayId, wlDisplayGetRegistry)
	if id, _ := req.uint32(); id != wlRegistryId {
		t.Fatalf("registry id = %d, want %d", id, wlRegistryId)
	}

	// Globals: an unrelated one, then the toplevel manager at a newer version.
	compositor.send(
		wlEventBytes(wlRegistryId, wlRegistryGlobal, func(m *wlMessage) {
			m.putUint32(1)
			m.putString("wl_compositor")
			m.putUint32(6)
		}),
		wlEventBytes(wlRegistryId, wlRegistryGlobal, func(m *wlMessage) {
			m.putUint32(managerName)
			m.putString(wlToplevelManagerInterface)
			m.putUint32(9)
		}),
	)
	bind := compositor.expectRequest(wlRegistryId, wlRegistryBind)
	name, _ := bind.uint32()
	iface, _ := bind.string()
	version, _ := bind.uint32()
	id, _ := bind.uint32()
	if name != managerName || iface != wlToplevelManagerInterface || version != wlToplevelManagerVersion || id != wlManagerId {
		t.Fatalf("bind(%d, %q, %d, %d), want (%d, %q, %d, %d)", name, iface, version, id,
			managerName, wlToplevelManagerInterface, wlToplevelManagerVersion, wlManagerId)
	}

	// Two windows; the editor is activated.
	newToplevel := func(handle uint32) []byte {
		return wlEventBytes(wlManagerId, wlManagerToplevel, func(m *wlMessage) { m.putUint32(handle) })
	}
	compositor.send(
		newToplevel(editor), wlAppIdEvent(editor, "org.gnome.TextEditor"),
		wlEventBytes(editor, wlHandleState, wlStateArray(0, wlToplevelStateActivated)),
		wlEventBytes(editor, wlHandleDone, nil),
	)
	wantFocus(t, focus, "org.gnome.TextEditor")

	// The browser opens in the background: no change of focus.
	compositor.send(
		newToplevel(browser), wlAppIdEvent(browser, "firefox"),
		wlEventBytes(browser, wlHandleState, wlStateArray()),
		wlEventBytes(browser, wlHandleDone, nil),
	)
	// Focus moves to the browser: the editor is deactivated first.
	compositor.send(
		wlEventBytes(editor, wlHandleState, wlStateArray()),
		wlEventBytes(editor, wlHandleDone, nil),
	)
	wantFocus(t, focus, "")
	compositor.send(
		wlEventBytes(browser, wlHandleState, wlStateArray(wlToplevelStateActivated)),
		wlEventBytes(browser, wlHandleDone, nil),
	)
	wantFocus(t, focus, "firefox")

	// Closing the focused window clears the focus and frees the handle.
	compositor.send(wlEventBytes(browser, wlHandleClosed, nil))
	wantFocus(t, focus, "")
	compositor.expectRequest(browser, wlHandleDestroy)

	compositor.send(wlEventBytes(wlManagerId, wlManagerFinished, nil))
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("watcher ended with %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("watcher should stop when the manager is finished")
	}
}

func TestWlFocusWatcherReportsProtocolErrors(t *testing.T) {
	compositor, _, done := startWatcher(t)
	compositor.expectRequest(wlDisplayId, wlDisplayGetRegistry)
	compositor.send(wlEventBytes(wlDisplayId, wlDisplayError, func(m *wlMessage) {
		m.putUint32(wlRegistryId)
		m.putUint32(1)
		m.putString("invalid arguments")
	}))
	select {
	case err := <-done:
		if err == nil || err == io.EOF {
			t.Fatalf("watcher ended with %v, want the protocol error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("watcher should stop on a protocol error")
	}
}

func TestWlSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	cases := []struct{ display, want string }{
		{"", "/run/user/1000/wayland-0"},
		{"wayland-1", "/run/user/1000/wayland-1"},
		{"/tmp/custom-socket", "/tmp/custom-socket"},
	}
	for _, tc := range cases {
		t.Setenv("WAYLAND_DISPLAY", tc.display)
		if got, err := wlSocketPath(); err != nil || got != tc.want {
			t.Errorf("WAYLAND_DISPLAY=%q: path = %q, %v; want %q", tc.display, got, err, tc.want)
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if _, err := wlSocketPath(); err == nil {
		t.Errorf("a relative socket name without XDG_RUNTIME_DIR must fail")
	}
}
