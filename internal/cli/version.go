package cli

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func buildInfoFallback() (version, commit, date, modified string) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", "", ""
	}
	version = strings.TrimSpace(bi.Main.Version)
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	return version, commit, date, modified
}

func (a *App) cmdVersion() error {
	version := strings.TrimSpace(buildVersion)
	commit := strings.TrimSpace(buildCommit)
	date := strings.TrimSpace(buildDate)

	fv, fc, fd, modified := buildInfoFallback()
	if version == "" || version == "dev" {
		if fv != "" {
			version = fv
		}
	}
	if commit == "" {
		commit = fc
	}
	if date == "" {
		date = fd
	}

	meta := strings.TrimSpace(strings.Join([]string{commit, date}, " "))
	if meta != "" && modified == "true" {
		meta += " dirty"
	}
	if meta != "" {
		a.printlnf("gym %s (%s)", version, meta)
		return nil
	}
	if version == "" {
		version = "dev"
	}
	a.printlnf("gym %s", version)
	return nil
}

// VersionLDFlags returns ldflags suitable for embedding build metadata.
// This is a helper for docs/tooling; it is not used by the CLI itself.
func VersionLDFlags(version, commit, date string) string {
	pkg := "hop.top/gym/internal/cli"
	return fmt.Sprintf("-X %s.buildVersion=%s -X %s.buildCommit=%s -X %s.buildDate=%s", pkg, version, pkg, commit, pkg, date)
}
