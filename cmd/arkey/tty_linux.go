//go:build linux

package main

import "golang.org/x/sys/unix"

// ioctlReadTermios is the request that reads terminal attributes. Linux and
// macOS spell it differently; isTerminal uses whichever this build provides.
const ioctlReadTermios = unix.TCGETS
