package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/unityfs"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// spliceTable replaces the string values of the keys in repl inside the
// language table lang of the Localization document, touching only those
// string literals: every other byte of doc (layout, other languages, metadata)
// stays as it was. It returns the new document, the number of values replaced
// and the keys of repl that the table does not have.
func spliceTable(doc []byte, lang string, repl map[string]string) (out []byte, n int, missing []string, err error) {
	bom := 0
	if bytes.HasPrefix(doc, []byte("\xef\xbb\xbf")) {
		bom = 3
	}
	dec := json.NewDecoder(bytes.NewReader(doc[bom:]))
	if err := expectDelim(dec, '{'); err != nil {
		return nil, 0, nil, err
	}
	type edit struct {
		start, end int
		lit        []byte
	}
	var edits []edit
	found := false
	seen := map[string]bool{}
	for dec.More() {
		name, err := keyToken(dec)
		if err != nil {
			return nil, 0, nil, err
		}
		if name != lang {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, 0, nil, err
			}
			continue
		}
		found = true
		if err := expectDelim(dec, '{'); err != nil {
			return nil, 0, nil, fmt.Errorf("language %s: %w", lang, err)
		}
		for dec.More() {
			k, err := keyToken(dec)
			if err != nil {
				return nil, 0, nil, err
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, 0, nil, err
			}
			text, ok := repl[k]
			if !ok {
				continue
			}
			seen[k] = true
			lit, err := marshalNoEscape(text)
			if err != nil {
				return nil, 0, nil, err
			}
			end := int(dec.InputOffset()) + bom
			edits = append(edits, edit{end - len(raw), end, lit})
		}
		if err := expectDelim(dec, '}'); err != nil {
			return nil, 0, nil, err
		}
	}
	if !found {
		return nil, 0, nil, fmt.Errorf("no language %q in the document", lang)
	}
	out = append([]byte(nil), doc...)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = append(out[:e.start], append(append([]byte(nil), e.lit...), out[e.end:]...)...)
	}
	for k := range repl {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	return out, len(edits), missing, nil
}

// patchCatalog updates the Addressables binary catalog record of one bundle.
// A record is [hash ptr][hash text ptr][CRC u32][size u32] followed by
// pointers to the options type, the provider, the bundle name and the
// internal id. The CRC covers the original bundle and would reject the
// modified one, so it is zeroed (0 means "do not check") and the size is set
// to the new file size. The record is found by the old size and confirmed by
// one of the pointers after it leading to the bundle name; anything but
// exactly one match is an error.
func patchCatalog(cat []byte, bundleName string, oldSize, newSize int) ([]byte, error) {
	needle := binary.LittleEndian.AppendUint32(nil, uint32(oldSize))
	var at []int
	for i := 0; ; {
		j := bytes.Index(cat[i:], needle)
		if j < 0 {
			break
		}
		p := i + j
		i = p + 1
		if p < 4 || p+4+4*recordPtrs > len(cat) {
			continue
		}
		for k := 0; k < recordPtrs; k++ {
			ptr := int(binary.LittleEndian.Uint32(cat[p+4+4*k:]))
			if ptr > 0 && ptr < len(cat) && bytes.HasPrefix(cat[ptr:], []byte(bundleName)) {
				at = append(at, p)
				break
			}
		}
	}
	if len(at) != 1 {
		return nil, fmt.Errorf("found %d catalog records for %s (size %d), want exactly 1", len(at), bundleName, oldSize)
	}
	out := append([]byte(nil), cat...)
	binary.LittleEndian.PutUint32(out[at[0]-4:], 0)
	binary.LittleEndian.PutUint32(out[at[0]:], uint32(newSize))
	return out, nil
}

// recordPtrs is how many pointer fields after the size are searched for the
// bundle name.
const recordPtrs = 6

// runMod builds a drop-in mod: translated XLIFF targets overwrite one
// language of the game's Localization asset, and the changed resources.assets
// is written under <out> with the same relative path as in the game folder,
// so the folder can be copied over the game root.
func runMod(args []string) error {
	fl := flag.NewFlagSet("mod", flag.ExitOnError)
	game := fl.String("game", "", "game root folder, e.g. .../common/Restory (required)")
	xlf := fl.String("xliff", "", "translated XLIFF (required)")
	out := fl.String("out", "", "output folder, laid out like the game root (required)")
	lang := fl.String("replace", "Russian", "language table to overwrite")
	asset := fl.String("asset", "Localization", "name of the TextAsset holding all languages")
	alsoBundle := fl.Bool("bundle", false, "also patch the Addressables copy in restory.data and the catalog (the game does not read it)")
	fl.Parse(args)
	if *game == "" || *xlf == "" || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	datas, _ := filepath.Glob(filepath.Join(*game, "*_Data"))
	if len(datas) != 1 {
		return fmt.Errorf("expected one *_Data folder in %s, found %d", *game, len(datas))
	}
	var replaced int
	aaRel := filepath.Join(filepath.Base(datas[0]), "StreamingAssets", "aa")
	aa := filepath.Join(*game, aaRel)

	fh, err := os.Open(*xlf)
	if err != nil {
		return err
	}
	xf, problems, err := xliff.Read(fh)
	fh.Close()
	if err != nil {
		return err
	}
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "warn: %s: target ignored: %s\n", p.ID, p.Reason)
	}
	repl := map[string]string{}
	for _, u := range xf.Units {
		if u.Target != nil && strings.TrimSpace(xliff.Plain(u.Target)) != "" {
			repl[u.ID] = xliff.Plain(u.Target)
		}
	}
	if len(repl) == 0 {
		return fmt.Errorf("%s has no translated targets", *xlf)
	}

	patch := func(script []byte) ([]byte, error) {
		doc, n, missing, err := spliceTable(script, *lang, repl)
		if err != nil {
			return nil, err
		}
		if _, err := parseLoc(doc); err != nil { // the patched document must still parse
			return nil, fmt.Errorf("patched %s does not parse: %w", *asset, err)
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "warn: %d XLIFF keys are not in the %s table and were skipped: %v\n", len(missing), *lang, missing)
		}
		replaced = n
		return doc, nil
	}

	// The game reads its Localization from resources.assets (Resources.Load);
	// the Addressables copy in restory.data is a duplicate it does not use.
	resRel := filepath.Join(filepath.Base(datas[0]), "resources.assets")
	res, err := patchResources(filepath.Join(*game, resRel), *asset, patch)
	if err != nil {
		return err
	}
	writes := []fileWrite{{filepath.Join(*out, resRel), res}}
	if *alsoBundle {
		bundleOut, bundle, catOut, cat, err := patchBundle(aa, aaRel, *out, *asset, patch)
		if err != nil {
			return err
		}
		writes = append(writes, fileWrite{bundleOut, bundle}, fileWrite{catOut, cat})
	}
	for _, w := range writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d bytes)\n", w.path, len(w.data))
	}
	fmt.Printf("replaced %d of %d %s strings\n", replaced, len(repl), *lang)
	return nil
}

type fileWrite struct {
	path string
	data []byte
}

// patchResources rewrites the named TextAsset inside a player data file
// (resources.assets) with patch(script) and returns the new file. Before
// returning it re-parses the result and checks that the asset has the new
// text and that every other object is byte-identical.
func patchResources(path, asset string, patch func([]byte) ([]byte, error)) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := unityfs.ParseFile(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i, o := range f.Objects {
		if o.ClassID != unityfs.ClassTextAsset {
			continue
		}
		name, script, err := o.TextAsset()
		if err != nil || name != asset {
			continue
		}
		doc, err := patch(script)
		if err != nil {
			return nil, err
		}
		nf, err := f.ReplaceTextAsset(o, doc)
		if err != nil {
			return nil, err
		}
		f2, err := unityfs.ParseFile(nf)
		if err != nil {
			return nil, fmt.Errorf("rebuilt %s does not parse: %w", path, err)
		}
		if len(f2.Objects) != len(f.Objects) {
			return nil, fmt.Errorf("rebuilt %s has %d objects, want %d", path, len(f2.Objects), len(f.Objects))
		}
		for j, o2 := range f2.Objects {
			if j == i {
				if _, s2, err := o2.TextAsset(); err != nil || !bytes.Equal(s2, doc) {
					return nil, fmt.Errorf("%s differs after rebuild", asset)
				}
				continue
			}
			if o2.PathID != f.Objects[j].PathID || !bytes.Equal(o2.Raw(), f.Objects[j].Raw()) {
				return nil, fmt.Errorf("object %d changed during rebuild", f.Objects[j].PathID)
			}
		}
		return nf, nil
	}
	return nil, fmt.Errorf("no TextAsset named %q in %s", asset, path)
}

// patchBundle does the same for the Addressables copy of the asset: it
// rebuilds the bundle and patches the catalog so the changed bundle is not
// rejected on CRC.
func patchBundle(aa, aaRel, out, asset string, patch func([]byte) ([]byte, error)) (bundleOut string, bundle []byte, catOut string, cat []byte, err error) {
	bundlePath, b, f, obj, err := locateTextAsset(aa, asset)
	if err != nil {
		return
	}
	m, err := obj.Read()
	if err != nil {
		return
	}
	script, _ := m["m_Script"].(string)
	doc, err := patch([]byte(script))
	if err != nil {
		return
	}
	nf, err := f.ReplaceTextAsset(obj, doc)
	if err != nil {
		return
	}
	return finishBundle(aa, aaRel, out, bundlePath, b, f, nf, func(bundle []byte) error {
		return verifyBundle(bundle, asset, doc)
	})
}

// finishBundle puts the rewritten serialized file nf into the bundle,
// serialises it, lets verify check the result (before anything is written to
// disk), and patches the catalog record of the bundle.
func finishBundle(aa, aaRel, out, bundlePath string, b *unityfs.Bundle, f *unityfs.File, nf []byte, verify func(bundle []byte) error) (bundleOut string, bundle []byte, catOut string, cat []byte, err error) {
	if err = b.SetNode(f.Name, nf); err != nil {
		return
	}
	bundle = b.Marshal()
	if err = verify(bundle); err != nil {
		err = fmt.Errorf("verification of the rebuilt bundle failed: %w", err)
		return
	}
	oldCat, err := os.ReadFile(filepath.Join(aa, "catalog.bin"))
	if err != nil {
		return
	}
	info, err := os.Stat(bundlePath)
	if err != nil {
		return
	}
	cat, err = patchCatalog(oldCat, filepath.Base(bundlePath), int(info.Size()), len(bundle))
	if err != nil {
		return
	}
	bundleOut = filepath.Join(out, aaRel, "StandaloneWindows64", filepath.Base(bundlePath))
	catOut = filepath.Join(out, aaRel, "catalog.bin")
	return
}

// locateTextAsset opens every bundle under aa and returns the one holding
// the named TextAsset together with the parsed pieces needed to rewrite it.
func locateTextAsset(aa, name string) (path string, b *unityfs.Bundle, f *unityfs.File, o *unityfs.Object, err error) {
	paths, err := findBundles(aa, "")
	if err != nil {
		return "", nil, nil, nil, err
	}
	for _, p := range paths {
		bu, err := unityfs.Open(p)
		if err != nil {
			continue
		}
		files, err := bu.Files()
		if err != nil {
			continue
		}
		for _, file := range files {
			for _, obj := range file.Objects {
				if obj.ClassID != unityfs.ClassTextAsset || !obj.HasTree() {
					continue
				}
				if m, err := obj.Read(); err == nil && m["m_Name"] == name {
					return p, bu, file, obj, nil
				}
			}
		}
	}
	return "", nil, nil, nil, fmt.Errorf("no TextAsset named %q under %s", name, aa)
}

// verifyBundle re-parses a rebuilt bundle and checks the asset's script.
func verifyBundle(data []byte, asset string, want []byte) error {
	b, err := unityfs.Parse(data)
	if err != nil {
		return err
	}
	files, err := b.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		for _, o := range f.Objects {
			if o.ClassID != unityfs.ClassTextAsset || !o.HasTree() {
				continue
			}
			if m, err := o.Read(); err == nil && m["m_Name"] == asset {
				if m["m_Script"] != string(want) {
					return fmt.Errorf("%s differs after rebuild", asset)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("%s missing after rebuild", asset)
}
