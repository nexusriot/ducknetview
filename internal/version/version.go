// Package version is the single source of truth for the release number: the
// Makefile, build-deb.sh and DEBIAN/control all read it from here, so a bump
// only ever needs to happen in this file.
package version

const Version = "0.3.0"
