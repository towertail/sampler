package scan

import "time"

var darwinRules = RuleSet{
	OS: "darwin",
	Rules: []Rule{
		{ID: "xcode-derived-data", Label: "Xcode DerivedData", Group: Safe, Match: wholeDir("~/Library/Developer/Xcode/DerivedData")},
		{ID: "simulator-caches", Label: "iOS Simulator caches", Group: Safe, Match: wholeDir("~/Library/Developer/CoreSimulator/Caches")},
		{ID: "npm-cache", Label: "npm cache", Group: Safe, Match: wholeDir("~/.npm/_cacache")},
		{ID: "yarn-cache", Label: "Yarn cache", Group: Safe, Match: wholeDir("~/Library/Caches/Yarn")},
		{ID: "pip-cache", Label: "pip cache", Group: Safe, Match: wholeDir("~/Library/Caches/pip")},
		{ID: "go-build-cache", Label: "Go build cache", Group: Safe, Match: wholeDir("~/Library/Caches/go-build")},
		{ID: "homebrew-cache", Label: "Homebrew downloads", Group: Safe, Match: wholeDir("~/Library/Caches/Homebrew")},
		{ID: "gradle-cache", Label: "Gradle caches", Group: Safe, Match: wholeDir("~/.gradle/caches")},
		{ID: "xcode-device-support", Label: "Xcode device support files", Group: Review, Match: wholeDir("~/Library/Developer/Xcode/iOS DeviceSupport")},
		{ID: "xcode-archives", Label: "Xcode archives", Group: Review, Match: wholeDir("~/Library/Developer/Xcode/Archives")},
		{ID: "ollama-models", Label: "Ollama models", Group: Review, Match: wholeDir("~/.ollama/models")},
		{ID: "huggingface-hub", Label: "Hugging Face models", Group: Review, Match: wholeDir("~/.cache/huggingface/hub")},
		{ID: "lm-studio-models", Label: "LM Studio models", Group: Review, Match: anyDir("~/.lmstudio/models", "~/.cache/lm-studio/models")},
		{ID: "node-modules", Label: "node_modules in old projects", Group: Review, Match: staleNodeModules(90 * 24 * time.Hour)},
		{ID: "old-releases", Label: "Old releases", Group: Review, Match: oldReleases(4)},
		{ID: "installers", Label: "Installers in Downloads", Group: Review, Globs: []string{"*.dmg", "*.pkg"}, Match: dirGlobs("~/Downloads", "*.dmg", "*.pkg")},
	},
	Never: []string{
		"~/.Trash",
		"~/Library/Containers/com.docker.docker",
		"~/Library/Application Support/Postgres",
		"~/Library/Mobile Documents",
		"/opt/homebrew/var",
		"/usr/local/var",
		"/System",
	},
}
