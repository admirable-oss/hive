// Package buildinfo reports which build of Hive is running. Release builds
// set the values with -ldflags "-X"; other builds fall back to what the Go
// toolchain embeds (module version for `go install`, VCS data for `go build`).
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set by the release build:
//
//	-X github.com/admirable-oss/hive/internal/buildinfo.version=v0.2.0
//	-X github.com/admirable-oss/hive/internal/buildinfo.commit=<sha>
//	-X github.com/admirable-oss/hive/internal/buildinfo.date=<RFC 3339>
var (
	version = ""
	commit  = ""
	date    = ""
)

// Info identifies a build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the running build's identity.
func Get() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && info.Commit == "":
				info.Commit = s.Value
			case s.Key == "vcs.time" && info.Date == "":
				info.Date = s.Value
			case s.Key == "vcs.modified" && s.Value == "true" && version == "":
				info.Commit += "-dirty"
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

// String is the one-line form printed by `hive version`.
func (i Info) String() string {
	s := "hive " + i.Version
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		s += fmt.Sprintf(" (%s", c)
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return s + " " + i.GoVersion + " " + i.Platform
}
