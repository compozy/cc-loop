package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	version, commit, date := resolve(debug.ReadBuildInfo())
	return fmt.Sprintf("%s (commit=%s date=%s)", version, commit, date)
}

func resolve(info *debug.BuildInfo, ok bool) (string, string, string) {
	resolvedVersion := Version
	resolvedCommit := Commit
	resolvedDate := Date

	if ok && info != nil {
		if isDefaultVersion(resolvedVersion) && info.Main.Version != "" && info.Main.Version != "(devel)" {
			resolvedVersion = strings.TrimPrefix(info.Main.Version, "v")
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if isDefaultCommit(resolvedCommit) && setting.Value != "" {
					resolvedCommit = shortCommit(setting.Value)
				}
			case "vcs.time":
				if isDefaultDate(resolvedDate) && setting.Value != "" {
					resolvedDate = setting.Value
				}
			}
		}
	}

	return resolvedVersion, resolvedCommit, resolvedDate
}

func isDefaultVersion(value string) bool {
	return value == "" || value == "dev"
}

func isDefaultCommit(value string) bool {
	return value == "" || value == "none" || value == "unknown"
}

func isDefaultDate(value string) bool {
	return value == "" || value == "unknown"
}

func shortCommit(value string) string {
	if len(value) <= 7 {
		return value
	}
	return value[:7]
}
