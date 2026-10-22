// Command unityloc inspects Unity asset bundles and extracts localization
// data from them.
//
//	unityloc list -in "<game>/Restory_Data/StreamingAssets/aa" [-bundle data] [-class 49,114]
//	unityloc dump -in <bundle-or-dir> -bundle <substring> -path-id <id> -out <file.json>
//	unityloc export -in "<game>/Restory_Data/StreamingAssets/aa" -out <raw>
//	unityloc xliff -in <raw>/Localization.json -source Russian -target English -out <file.xlf>
//
// export pulls the "Localization" TextAsset (all languages in one JSON) out of
// the bundles and splits it into one flat <Language>.json per language; xliff
// builds an XLIFF 1.2 file from two of those languages; mod overwrites one
// language (Russian) with translated XLIFF targets and writes the changed
// bundle and catalog in a folder laid out like the game root.
//
//	unityloc mod -game "<game root>" -xliff <translated.xlf> -out <mod folder>
//	unityloc story-xliff -game "<game root>" -out <dialogue.xlf>
//	unityloc story-mod -game "<game root>" -xliff <translated-dialogue.xlf> -out <mod folder>
//
// story-xliff / story-mod handle the dialogue, which lives in the Dialogue
// System database "Restory Story": one XLIFF unit per entry (source = the "ru"
// field, optional target = another language field such as "en"), and the
// translation written back into the bundle plus the patched catalog.
//
// The game's files are only read.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/unityfs"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "list":
		err = runList(os.Args[2:])
	case "dump":
		err = runDump(os.Args[2:])
	case "export":
		err = runExport(os.Args[2:])
	case "xliff":
		err = runXLIFF(os.Args[2:])
	case "mod":
		err = runMod(os.Args[2:])
	case "story-xliff":
		err = runStoryXLIFF(os.Args[2:])
	case "story-mod":
		err = runStoryMod(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: unityloc list|dump [flags]")
	os.Exit(2)
}

// findBundles returns the .bundle files under in (or in itself if a file)
// whose path contains the filter substring.
func findBundles(in, filter string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(in, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".bundle") && strings.Contains(filepath.Base(p), filter) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// objectName returns m_Name for the object kinds that carry one, or "".
func objectName(o *unityfs.Object) string {
	if (o.ClassID != unityfs.ClassTextAsset && o.ClassID != unityfs.ClassMonoBehavior && o.ClassID != 128) || !o.HasTree() || o.Size > 16<<20 {
		return ""
	}
	m, err := o.Read()
	if err != nil {
		return "<" + err.Error() + ">"
	}
	s, _ := m["m_Name"].(string)
	return s
}

func runList(args []string) error {
	fl := flag.NewFlagSet("list", flag.ExitOnError)
	in := fl.String("in", "", "bundle file or folder to scan (required)")
	filter := fl.String("bundle", "", "only bundles whose file name contains this")
	classes := fl.String("class", "", "comma-separated class ids to list (default: summary only)")
	fl.Parse(args)
	if *in == "" {
		fl.Usage()
		os.Exit(2)
	}
	want := map[int32]bool{}
	for _, c := range strings.Split(*classes, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
			want[int32(n)] = true
		}
	}
	paths, err := findBundles(*in, *filter)
	if err != nil {
		return err
	}
	for _, p := range paths {
		b, err := unityfs.Open(p)
		if err != nil {
			fmt.Printf("%s: %v\n", filepath.Base(p), err)
			continue
		}
		files, err := b.Files()
		if err != nil {
			fmt.Printf("%s: %v\n", filepath.Base(p), err)
			continue
		}
		counts := map[int32]int{}
		for _, f := range files {
			for _, o := range f.Objects {
				counts[o.ClassID]++
				if want[o.ClassID] {
					fmt.Printf("%s\t%s\tid=%d\tclass=%d\t%dB\t%s\n", filepath.Base(p), f.Name, o.PathID, o.ClassID, o.Size, objectName(o))
				}
			}
		}
		fmt.Printf("%s: unity %s, %d serialized file(s), classes %v\n", filepath.Base(p), b.UnityVersion, len(files), counts)
	}
	return nil
}

func runDump(args []string) error {
	fl := flag.NewFlagSet("dump", flag.ExitOnError)
	in := fl.String("in", "", "bundle file or folder (required)")
	filter := fl.String("bundle", "", "bundle file name contains this")
	id := fl.Int64("path-id", 0, "object path id (required)")
	out := fl.String("out", "", "output .json file (required)")
	fl.Parse(args)
	if *in == "" || *id == 0 || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	paths, err := findBundles(*in, *filter)
	if err != nil {
		return err
	}
	for _, p := range paths {
		b, err := unityfs.Open(p)
		if err != nil {
			continue
		}
		files, err := b.Files()
		if err != nil {
			continue
		}
		for _, f := range files {
			for _, o := range f.Objects {
				if o.PathID != *id {
					continue
				}
				m, err := o.Read()
				if err != nil {
					return err
				}
				data, err := json.MarshalIndent(m, "", "  ")
				if err != nil {
					return err
				}
				return os.WriteFile(*out, data, 0o644)
			}
		}
	}
	return fmt.Errorf("object %d not found", *id)
}
