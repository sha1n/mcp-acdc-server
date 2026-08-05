package content

import (
	"os"
	"path/filepath"
	"strings"
)

// LegacyPath pairs a superseded layout name with its replacement. Both are
// slash-separated and relative to the content root.
type LegacyPath struct {
	Old string
	New string
}

// legacyLayout is the transitional mapping from the superseded mcp-* convention
// to the current one, in the order it is reported to the operator. This file
// exists to be deleted once the transition window closes.
var legacyLayout = []LegacyPath{
	{Old: "mcp-metadata.yaml", New: ConfigDirName + "/" + ConfigFileName},
	{Old: "mcp-prompts/", New: ConfigDirName + "/" + PromptsDirName + "/"},
	{Old: "mcp-resources/", New: ConfigDirName + "/" + ResourcesDirName + "/"},
}

// DetectLegacyLayout reports superseded layout entries present at the content
// root. An entry counts as present whatever its type: reporting a stale name is
// the job here, classifying what occupies it is not. os.Lstat rather than
// os.Stat so a dangling symlink still counts as an occupied name.
//
// The mcp-resources/ marker is reported only when a manifest also exists, at
// either the legacy or current location. Zero-config discovery never scans
// mcp-resources/ regardless — a non-nil Index always routes it to the
// configured-index path instead — so a bare mcp-resources/ with no manifest
// anywhere changes nothing silently, and mcp-resources/ is too plausible a
// directory name in an unrelated repository to refuse startup over on its
// own. mcp-metadata.yaml and mcp-prompts/ have no such exception: a stale
// manifest silently changes server identity and the indexed file set, and
// prompts.DiscoverPrompts reads PromptsDir unconditionally, so prompts vanish.
func DetectLegacyLayout(contentDir string) []LegacyPath {
	manifestPresent := entryExists(filepath.Join(contentDir, "mcp-metadata.yaml")) ||
		entryExists(filepath.Join(contentDir, ConfigDirName, ConfigFileName))

	var found []LegacyPath
	for _, candidate := range legacyLayout {
		name := strings.TrimSuffix(candidate.Old, "/")
		if !entryExists(filepath.Join(contentDir, name)) {
			continue
		}
		if candidate.Old == "mcp-resources/" && !manifestPresent {
			continue
		}
		found = append(found, candidate)
	}
	return found
}

func entryExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
