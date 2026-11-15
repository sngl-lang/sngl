//go:build !linux

package main

import "time"

func lutimes(path string, t time.Time) error {
	return errNoLutimes
}
