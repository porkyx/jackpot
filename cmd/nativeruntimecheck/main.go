// nativeruntimecheck drives the production preflight decision and native notice
// with a controlled missing/error probe; it never changes the installed runtime.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/porkyx/jackpot/internal/platform"
)

func main() {
	mode := flag.String("mode", "installed", "installed, missing or error probe")
	flag.Parse()
	if *mode != "installed" && *mode != "missing" && *mode != "error" {
		fmt.Fprintln(os.Stderr, "invalid controlled runtime mode")
		os.Exit(2)
	}
	realVersion, realErr := platform.DetectWebView2Runtime()
	if realErr != nil || realVersion == "" {
		fmt.Fprintln(os.Stderr, "this safe diagnostic requires an installed local runtime")
		os.Exit(2)
	}
	probe := platform.DetectWebView2Runtime
	if *mode == "missing" {
		probe = func() (string, error) { return "", nil }
	}
	if *mode == "error" {
		probe = func() (string, error) { return "", errors.New("controlled private registry path sentinel") }
	}
	shown := 0
	err := platform.CheckWebView2Runtime(probe, func(title, message string) error { shown++; return platform.ShowWebView2RuntimeNotice(title, message) })
	decision := "installed"
	if errors.Is(err, platform.ErrWebView2Missing) {
		decision = "missing"
	}
	if errors.Is(err, platform.ErrWebView2Unavailable) {
		decision = "unavailable"
	}
	if errors.Is(err, platform.ErrWebView2Notice) {
		fmt.Fprintln(os.Stderr, "native runtime notice failed")
		os.Exit(3)
	}
	if (*mode == "installed" && err != nil) || (*mode == "missing" && decision != "missing") || (*mode == "error" && decision != "unavailable") {
		fmt.Fprintln(os.Stderr, "runtime preflight decision mismatch")
		os.Exit(3)
	}
	report := struct {
		Mode             string `json:"mode"`
		Decision         string `json:"decision"`
		InstalledVersion string `json:"installedVersion"`
		NativeNotices    int    `json:"nativeNotices"`
		AllowedDataOpen  bool   `json:"allowedDataOpen"`
		PhysicalMissing  bool   `json:"physicalMissing"`
		NetworkInstall   bool   `json:"networkInstall"`
	}{*mode, decision, realVersion, shown, err == nil, false, false}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "report failed")
		os.Exit(3)
	}
}
