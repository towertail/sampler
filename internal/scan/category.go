package scan

import (
	"strings"

	"github.com/towertail/sampler/pkg/schema"
)

// catPaths gives a category to folders by path. Keys that start with ~/
// are below the home folder. Keys use / on all OSes.
var catPaths = map[string]map[string]string{
	"darwin": {
		"~/.ollama":                             schema.ScanCatAI,
		"~/.cache/huggingface":                  schema.ScanCatAI,
		"~/.cache/lm-studio":                    schema.ScanCatAI,
		"~/.lmstudio":                           schema.ScanCatAI,
		"~/Library/Application Support/ChatGPT": schema.ScanCatAI,
		"~/.npm":                                schema.ScanCatCache,
		"~/.cache":                              schema.ScanCatCache,
		"~/.gradle":                             schema.ScanCatCache,
		"~/.m2":                                 schema.ScanCatCache,
		"~/Library/Caches":                      schema.ScanCatCache,
		"~/.android":                            schema.ScanCatSDK,
		"~/Library/Android":                     schema.ScanCatSDK,
		"~/Library/Developer":                   schema.ScanCatSDK,
		"~/.rustup":                             schema.ScanCatSDK,
		"~/.cargo":                              schema.ScanCatSDK,
		"~/go/pkg":                              schema.ScanCatSDK,
		"~/.nvm":                                schema.ScanCatSDK,
		"~/.pyenv":                              schema.ScanCatSDK,
		"~/.sdkman":                             schema.ScanCatSDK,
		"~/miniconda3":                          schema.ScanCatSDK,
		"~/anaconda3":                           schema.ScanCatSDK,
		"~/Documents":                           schema.ScanCatDocs,
		"~/Downloads":                           schema.ScanCatDocs,
		"~/Pictures":                            schema.ScanCatDocs,
		"~/Movies":                              schema.ScanCatDocs,
		"~/Music":                               schema.ScanCatDocs,
		"/Applications":                         schema.ScanCatSDK,
		"/opt/homebrew":                         schema.ScanCatSDK,
		"/usr/local":                            schema.ScanCatSDK,
	},
	"linux": {
		"~/.ollama":            schema.ScanCatAI,
		"~/.cache/huggingface": schema.ScanCatAI,
		"/usr/share/ollama":    schema.ScanCatAI,
		"~/.npm":               schema.ScanCatCache,
		"~/.cache":             schema.ScanCatCache,
		"~/.gradle":            schema.ScanCatCache,
		"~/.m2":                schema.ScanCatCache,
		"/var/cache":           schema.ScanCatCache,
		"/var/lib/docker":      schema.ScanCatSDK,
		"~/.rustup":            schema.ScanCatSDK,
		"~/.cargo":             schema.ScanCatSDK,
		"~/go/pkg":             schema.ScanCatSDK,
		"~/.nvm":               schema.ScanCatSDK,
		"~/.pyenv":             schema.ScanCatSDK,
		"~/.sdkman":            schema.ScanCatSDK,
		"~/miniconda3":         schema.ScanCatSDK,
		"~/anaconda3":          schema.ScanCatSDK,
		"/opt":                 schema.ScanCatSDK,
		"/usr/local/go":        schema.ScanCatSDK,
		"~/Documents":          schema.ScanCatDocs,
		"~/Downloads":          schema.ScanCatDocs,
		"~/Pictures":           schema.ScanCatDocs,
		"~/Videos":             schema.ScanCatDocs,
		"~/Music":              schema.ScanCatDocs,
	},
	"windows": {
		"~/.ollama":                 schema.ScanCatAI,
		"~/.cache/huggingface":      schema.ScanCatAI,
		"~/.lmstudio":               schema.ScanCatAI,
		"~/AppData/Local/Temp":      schema.ScanCatCache,
		"~/AppData/Local/npm-cache": schema.ScanCatCache,
		"~/AppData/Local/pip/cache": schema.ScanCatCache,
		"~/AppData/Local/NuGet":     schema.ScanCatCache,
		"~/.nuget":                  schema.ScanCatCache,
		"~/.gradle":                 schema.ScanCatCache,
		"~/.m2":                     schema.ScanCatCache,
		"~/.cache":                  schema.ScanCatCache,
		"~/AppData/Local/Android":   schema.ScanCatSDK,
		"~/AppData/Local/Docker":    schema.ScanCatSDK,
		"~/.android":                schema.ScanCatSDK,
		"~/.rustup":                 schema.ScanCatSDK,
		"~/.cargo":                  schema.ScanCatSDK,
		"~/go/pkg":                  schema.ScanCatSDK,
		"~/Documents":               schema.ScanCatDocs,
		"~/Downloads":               schema.ScanCatDocs,
		"~/Pictures":                schema.ScanCatDocs,
		"~/Videos":                  schema.ScanCatDocs,
		"~/Music":                   schema.ScanCatDocs,
		"C:/Program Files":          schema.ScanCatSDK,
		"C:/Program Files (x86)":    schema.ScanCatSDK,
	},
}

// categorize sets the category of each node of the pruned tree. A path
// rule wins. Then a folder with a project marker is code. Then a folder
// gets the category of its parent. A folder with no category of its
// own whose children are more than half code is code too.
func (t *Tree) categorize(home string) {
	paths := map[string]string{}
	for k, v := range catPaths[t.OS] {
		paths[t.norm(t.expand(k, home))] = v
	}
	var walk func(n *node, parent string) (code int64)
	walk = func(n *node, parent string) int64 {
		own := ""
		for _, p := range n.paths {
			if c, ok := paths[t.norm(p)]; ok {
				own = c
			}
		}
		if own == "" && n.dir != nil && n.dir.marks != 0 {
			own = schema.ScanCatCode
		}
		n.cat = own
		if n.cat == "" {
			n.cat = parent
		}
		var code int64
		for _, k := range n.kids {
			code += walk(k, n.cat)
		}
		if own == "" && n.cat == schema.ScanCatOther && n.bytes > 0 && code*2 > n.bytes {
			n.cat = schema.ScanCatCode
			for _, k := range n.kids {
				if k.other {
					k.cat = schema.ScanCatCode
				}
			}
		}
		if n.cat == schema.ScanCatCode {
			return n.bytes
		}
		return code
	}
	walk(t.pruned, schema.ScanCatOther)
}

// expand turns a key of catPaths or of a rule into a full path.
func (t *Tree) expand(p, home string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		p = strings.TrimSuffix(home, t.sep) + t.sep + rest
	} else if p == "~" {
		p = home
	}
	if t.sep != "/" {
		p = strings.ReplaceAll(p, "/", t.sep)
	}
	return p
}

// norm makes a path key for a map lookup.
func (t *Tree) norm(p string) string {
	if t.OS == "windows" || t.OS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}
