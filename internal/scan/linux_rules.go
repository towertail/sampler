package scan

import "time"

var linuxRules = RuleSet{
	OS: "linux",
	Rules: []Rule{
		{ID: "apt-archives", Label: "APT package cache", Group: Safe, Elevated: true, Globs: []string{"*.deb"}, Match: dirGlobs("/var/cache/apt/archives", "*.deb")},
		{ID: "journal-archives", Label: "Archived systemd journals", Group: Safe, Elevated: true, Globs: []string{"*@*.journal"}, Match: subtreeGlob("/var/log/journal", "*@*.journal")},
		{ID: "rotated-logs", Label: "Rotated logs", Group: Safe, Elevated: true, Globs: []string{"*.gz"}, Match: subtreeGlob("/var/log", "*.gz")},
		{ID: "crash-reports", Label: "Crash reports", Group: Safe, Elevated: true, Match: dirContents("/var/crash")},
		{ID: "npm-cache", Label: "npm cache", Group: Safe, Match: wholeDir("~/.npm/_cacache")},
		{ID: "yarn-cache", Label: "Yarn cache", Group: Safe, Match: wholeDir("~/.cache/yarn")},
		{ID: "pip-cache", Label: "pip cache", Group: Safe, Match: wholeDir("~/.cache/pip")},
		{ID: "go-build-cache", Label: "Go build cache", Group: Safe, Match: wholeDir("~/.cache/go-build")},
		{ID: "gradle-cache", Label: "Gradle caches", Group: Safe, Match: wholeDir("~/.gradle/caches")},
		{ID: "thumbnails", Label: "Thumbnail cache", Group: Safe, Match: wholeDir("~/.cache/thumbnails")},
		{ID: "ollama-models", Label: "Ollama models", Group: Review, Match: wholeDir("~/.ollama/models")},
		{ID: "ollama-service-models", Label: "Ollama models (service)", Group: Review, Elevated: true, Match: wholeDir("/usr/share/ollama/.ollama/models")},
		{ID: "huggingface-hub", Label: "Hugging Face models", Group: Review, Match: wholeDir("~/.cache/huggingface/hub")},
		{ID: "node-modules", Label: "node_modules in old projects", Group: Review, Match: staleNodeModules(90 * 24 * time.Hour)},
		{ID: "old-releases", Label: "Old releases", Group: Review, Match: oldReleases(4)},
		{ID: "installers", Label: "Packages in Downloads", Group: Review, Globs: []string{"*.deb", "*.rpm", "*.AppImage"}, Match: dirGlobs("~/Downloads", "*.deb", "*.rpm", "*.AppImage")},
	},
	Never: []string{
		"~/.local/share/Trash",
		"/var/lib/docker",
		"/var/lib/containerd",
		"/var/lib/postgresql",
		"/var/lib/mysql",
		"/var/lib/mongodb",
		"/var/lib/redis",
		"/boot",
		"/proc",
		"/sys",
	},
}
