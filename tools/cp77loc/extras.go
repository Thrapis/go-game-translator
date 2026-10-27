package main

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/Thrapis/go-game-translator/internal/game/cyberpunk2077"
	"github.com/Thrapis/go-game-translator/internal/wolvenkit"
)

// placeholderGaps finds the variants of src that are only a VO placeholder
// ("[en_us]Hey.") although the same variant of other has real text. Those
// strings are normally not exported, since the placeholder is no source text,
// but a mod built on other's language needs them translated. The result maps
// the variant key to the placeholder without its language tag, the English
// text the translator works from.
func placeholderGaps(src, other *wolvenkit.File) map[string]string {
	have := make(map[string]wolvenkit.Entry, len(other.Entries))
	for _, e := range other.Entries {
		if _, dup := have[e.ID]; !dup {
			have[e.ID] = e
		}
	}
	out := map[string]string{}
	for _, e := range src.Entries {
		o, ok := have[e.ID]
		if !ok {
			continue
		}
		for _, male := range []bool{false, true} {
			s, t := e.Female, o.Female
			if male {
				s, t = e.Male, o.Male
			}
			if !cyberpunk2077.IsVOPlaceholder(s) || t == "" || !portable(t) {
				continue
			}
			out[e.Key(male)] = cyberpunk2077.StripVOPlaceholder(s)
		}
	}
	return out
}

// extrasFor runs placeholderGaps for the resource rel (a path under srcDir in
// the tree at root) against the same resource under otherDir. It returns nil
// when otherDir is empty or the other file does not exist.
func extrasFor(root, rel, srcDir, otherDir string, f *wolvenkit.File) (map[string]string, error) {
	if otherDir == "" {
		return nil, nil
	}
	otherRel, ok := swapSegment(rel, srcDir, otherDir)
	if !ok {
		return nil, nil
	}
	o, err := parseFile(filepath.Join(root, otherRel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return placeholderGaps(f, o), nil
}
