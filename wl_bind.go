package main

import (
	"encoding/binary"
	"unsafe"

	wl "github.com/dkolbly/wl"
)

// registryBindOpcode is wl_registry.bind.
const registryBindOpcode = 0

// wlByteOrder is the host byte order, which is what the Wayland wire uses.
var wlByteOrder = func() binary.ByteOrder {
	var probe uint16 = 1
	if *(*byte)(unsafe.Pointer(&probe)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}()

// wlStringArgs encodes a string argument for the Wayland wire as 32-bit
// words: the length including the terminating NUL, then the bytes padded with
// NULs to a multiple of four.
func wlStringArgs(s string) []interface{} {
	var length = len(s) + 1
	var padded = make([]byte, (length+3)&^3)
	copy(padded, s)
	var args = []interface{}{uint32(length)}
	for i := 0; i < len(padded); i += 4 {
		args = append(args, wlByteOrder.Uint32(padded[i:i+4]))
	}
	return args
}

// wlRegistryBind binds a global to proxy.
//
// It replaces wl.Registry.Bind, whose string encoding declares the padded
// size as the string length. libwayland rejects that ("string has embedded
// nul") and closes the connection for any interface name whose length is not
// 3 modulo 4, which includes zwlr_foreign_toplevel_manager_v1. The library
// then exits the whole process, so on compositors that offer this global
// (sway, Hyprland and other wlroots ones) the input method died on startup.
func wlRegistryBind(registry *wl.Registry, name uint32, iface string, version uint32, proxy wl.Proxy) error {
	var args = []interface{}{name}
	args = append(args, wlStringArgs(iface)...)
	args = append(args, version, proxy)
	return registry.Context().SendRequest(registry, registryBindOpcode, args...)
}
