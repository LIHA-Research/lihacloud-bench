package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestApplyModuleInfoUsesTaggedModuleMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = originalVersion, originalCommit, originalDate })
	Version, Commit, Date = "dev", "unknown", "unknown"

	applyModuleInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-08-11T13:00:00Z"},
		},
	})

	if Version != "v0.1.0" || Commit != "0123456789abcdef" || Date != "2026-08-11T13:00:00Z" {
		t.Fatalf("unexpected build metadata: version=%q commit=%q date=%q", Version, Commit, Date)
	}
}

func TestApplyModuleInfoPreservesInjectedValues(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = originalVersion, originalCommit, originalDate })
	Version, Commit, Date = "v9.9.9", "injected", "injected-date"

	applyModuleInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "module"},
			{Key: "vcs.time", Value: "module-date"},
		},
	})

	if Version != "v9.9.9" || Commit != "injected" || Date != "injected-date" {
		t.Fatalf("injected metadata was overwritten: version=%q commit=%q date=%q", Version, Commit, Date)
	}
}
