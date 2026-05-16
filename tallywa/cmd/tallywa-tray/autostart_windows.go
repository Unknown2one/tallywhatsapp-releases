//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

const autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autostartName = "TallyWhatsApp"

// autostartEnabled returns true if our Run entry is present and points
// at the current executable. We accept a mismatched path silently —
// the user may have moved the app, in which case we'll fix it on
// enable.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(autostartName)
	if err != nil {
		return false
	}
	return v != ""
}

func enableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Quote the exe path so spaces in install dirs don't break Windows'
	// command-line parser. No flags — the tray launches with sensible
	// defaults at logon.
	return k.SetStringValue(autostartName, strconv.Quote(exe))
}

func disableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(autostartName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
