package main

import (
	"flag"
	"fmt"
	"os"
	"tdev/internal/update"
)

func main() {
	dir := flag.String("dir", "dist", "directory of four prebuilt binaries")
	version := flag.String("version", "", "SemVer of the binaries")
	key := flag.String("private-key", "", "external PKCS8 Ed25519 signing key (never stored in this repo)")
	installer := flag.String("installer", "scripts/install.sh", "bootstrap installer template")
	releaseURL := flag.String("release-url", os.Getenv("TDEV_RELEASE_URL"), "embedded HTTPS release download directory")
	upgradeURL := flag.String("upgrade-url", os.Getenv("TDEV_UPGRADE_URL"), "embedded HTTPS upgrade directory")
	flag.Parse()
	if *key == "" || *version == "" {
		fmt.Fprintln(os.Stderr, "--version and --private-key are required")
		os.Exit(2)
	}
	if err := update.BuildRelease(*dir, *version, *key); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	template, err := os.ReadFile(*installer)
	if err == nil {
		err = update.BuildInstallerWithSource(*dir, string(template), *releaseURL, *upgradeURL)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
