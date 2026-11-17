//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// restart runs this program again in place, with the same arguments and environment.
func restart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// hangups delivers SIGHUP, which restarts the server in place (after a rebuild, say).
func hangups() <-chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	return c
}
