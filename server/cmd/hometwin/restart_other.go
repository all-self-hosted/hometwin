//go:build !unix

package main

import (
	"errors"
	"os"
)

func restart() error { return errors.New("the home changed: start hometwin again") }

// hangups never fires where there is no SIGHUP.
func hangups() <-chan os.Signal { return nil }
