//go:build !windows

package main

import "fmt"

func runGUI() {
	fmt.Println("MoreLink relay running in headless mode (no GUI on this platform).")
	select {}
}

func runGUIProtected() {
	// No GUI on non-Windows; runGUI blocks forever in headless mode.
	runGUI()
}
