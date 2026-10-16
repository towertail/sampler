package scan

import "time"

var windowsRules = RuleSet{
	OS: "windows",
	Rules: []Rule{
		{ID: "temp", Label: "Temporary files", Group: Safe, Match: dirContents("~/AppData/Local/Temp")},
		{ID: "crash-dumps", Label: "Crash dumps", Group: Safe, Match: wholeDir("~/AppData/Local/CrashDumps")},
		{ID: "npm-cache", Label: "npm cache", Group: Safe, Match: wholeDir("~/AppData/Local/npm-cache/_cacache")},
		{ID: "pip-cache", Label: "pip cache", Group: Safe, Match: wholeDir("~/AppData/Local/pip/cache")},
		{ID: "nuget-cache", Label: "NuGet HTTP cache", Group: Safe, Match: wholeDir("~/AppData/Local/NuGet/v3-cache")},
		{ID: "go-build-cache", Label: "Go build cache", Group: Safe, Match: wholeDir("~/AppData/Local/go-build")},
		{ID: "gradle-cache", Label: "Gradle caches", Group: Safe, Match: wholeDir("~/.gradle/caches")},
		{ID: "update-downloads", Label: "Windows Update downloads", Group: Safe, Elevated: true, Match: dirContents("C:/Windows/SoftwareDistribution/Download")},
		{ID: "ollama-models", Label: "Ollama models", Group: Review, Match: wholeDir("~/.ollama/models")},
		{ID: "huggingface-hub", Label: "Hugging Face models", Group: Review, Match: wholeDir("~/.cache/huggingface/hub")},
		{ID: "lm-studio-models", Label: "LM Studio models", Group: Review, Match: wholeDir("~/.lmstudio/models")},
		{ID: "node-modules", Label: "node_modules in old projects", Group: Review, Match: staleNodeModules(90 * 24 * time.Hour)},
		{ID: "old-releases", Label: "Old releases", Group: Review, Match: oldReleases(4)},
		{ID: "installers", Label: "Installers in Downloads", Group: Review, Globs: []string{"*.msi", "*.exe"}, Match: dirGlobs("~/Downloads", "*.msi", "*.exe")},
	},
	Never: []string{
		"C:/$Recycle.Bin",
		"~/AppData/Local/Docker",
		"C:/ProgramData/Docker",
		"C:/ProgramData/DockerDesktop",
		"C:/Program Files/PostgreSQL",
		"C:/ProgramData/MySQL",
		"C:/Windows/System32",
		"C:/Windows/WinSxS",
	},
}
