//go:build android

package gui

import "errors"

// The phone reads a code with its own camera. Not built yet, and saying so is better than a button
// that does nothing when it is pressed.
func canScan() bool { return false }

func startScan(scanning) error { return errors.New("this device cannot scan yet") }

func stopScan() {}
