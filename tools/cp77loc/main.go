// Command cp77loc converts Cyberpunk 2077 localization between WolvenKit's
// JSON export (*.json.json) and Crowdin CSV (id,source,translation,context),
// the format the translator works on, and XLIFF 1.2 for review in a CAT tool.
//
//	cp77loc export -in raw -out flat          # JSON tree -> CSV tree
//	cp77loc xliff  -in flat-be -out xliff-be  # CSV tree -> XLIFF tree
//	cp77loc import -template raw -in flat-be -out be   # CSV or XLIFF tree -> JSON tree
//	cp77loc ref -in raw -ref-lang-dir en-us -out xliff-ru-en  # official loc as reference XLIFF
//	cp77loc verify -in raw                    # byte-exact re-encode check
//
// export writes one CSV per resource that has text, mirroring the tree
// (x.json.json -> x.csv). Rows: the female (default) variant under the entry
// id (primaryKey / stringId), the male variant under id@male. Empty strings,
// "[en_us]..." voice-over placeholders and the game's deliberately corrupted
// glitch text are not exported (import keeps them as they are).
// onscreens_final.json.json is skipped: it duplicates onscreens.json.json in
// the same folder. (Other "*_final" files are ordinary subtitle scenes.)
//
// xliff turns each CSV into an .xlf: engine markup becomes locked <ph> tags,
// the context column a <note>, and a filled translation a target in state
// needs-review-translation. Rows with no Cyrillic text are left out.
//
// ref pairs two language folders of one JSON tree (e.g. ru-ru and en-us) into
// reference XLIFF: the same units, ids and paths as xliff over the ru export,
// with the official localization as a "final" target that keeps its own tags,
// even where they differ from the source's. Reference only; not for import.
//
// import copies the template tree and writes every non-empty translation
// into its entry, touching nothing else. For x.json.json it reads x.xlf or
// x.csv from -in (not both). onscreens_final.json.json takes the translations
// of onscreens. Files without a translation file are copied unchanged.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/extract/crowdincsv"
	"github.com/Thrapis/go-game-translator/internal/game/cyberpunk2077"
	"github.com/Thrapis/go-game-translator/internal/wolvenkit"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

const (
	jsonExt = ".json.json"
	// finalName duplicates its sibling onscreensName; they share one CSV.
	finalName     = "onscreens_final" + jsonExt
	onscreensName = "onscreens" + jsonExt
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "export":
		err = cmdExport(args)
	case "xliff":
		err = cmdXLIFF(args)
	case "import":
		err = cmdImport(args)
	case "ref":
		err = cmdRef(args)
	case "verify":
		err = cmdVerify(args)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  cp77loc export -in <raw json tree> -out <csv tree>
  cp77loc xliff -in <csv tree> -out <xliff tree> [-source-lang ru] [-target-lang be]
  cp77loc import -template <raw json tree> -in <translated csv or xliff tree> -out <json tree>
  cp77loc ref -in <raw json tree> -ref-lang-dir en-us -out <xliff tree> [-source-dir ru-ru] [-source-lang ru] [-target-lang en]
  cp77loc verify -in <raw json tree>`)
	os.Exit(2)
}

func required(fs *flag.FlagSet, args []string, names ...string) {
	_ = fs.Parse(args)
	for _, n := range names {
		if fs.Lookup(n).Value.String() == "" {
			fmt.Fprintf(os.Stderr, "%s: -%s is required\n", fs.Name(), n)
			fs.Usage()
			os.Exit(2)
		}
	}
}

// isFinal reports whether rel is the on-screen duplicate onscreens_final.
func isFinal(rel string) bool { return filepath.Base(rel) == finalName }

// baseRel maps a resource path to its translation file path without the
// extension; onscreens_final shares the onscreens file.
func baseRel(rel string) string {
	if isFinal(rel) {
		rel = filepath.Join(filepath.Dir(rel), onscreensName)
	}
	return strings.TrimSuffix(rel, jsonExt)
}

func csvRel(rel string) string { return baseRel(rel) + ".csv" }

// walkFiles calls fn with the slash-free relative path of every file under root.
func walkFiles(root string, fn func(rel string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(rel)
	})
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// --- export ------------------------------------------------------------------

func cmdExport(args []string) error {
	fl := flag.NewFlagSet("export", flag.ExitOnError)
	in := fl.String("in", "", "WolvenKit JSON tree (required)")
	out := fl.String("out", "", "CSV tree to write (required)")
	required(fl, args, "in", "out")

	var files, rows int
	var sk skipped
	err := walkFiles(*in, func(rel string) error {
		if !strings.HasSuffix(rel, jsonExt) || isFinal(rel) {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(*in, rel))
		if err != nil {
			return err
		}
		f, err := wolvenkit.Parse(b)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		rs, dups := exportRows(f, &sk)
		if len(dups) > 0 {
			fmt.Fprintf(os.Stderr, "warn: %s: duplicate ids %v (import applies one translation to all)\n", rel, dups)
		}
		if len(rs) == 0 {
			return nil
		}
		var buf bytes.Buffer
		if err := crowdincsv.Write(&buf, rs); err != nil {
			return err
		}
		files++
		rows += len(rs)
		return writeFile(filepath.Join(*out, csvRel(rel)), buf.Bytes())
	})
	if err != nil {
		return err
	}
	fmt.Printf("exported %d rows into %d files (skipped: %d VO placeholders, %d glitch strings) -> %s\n",
		rows, files, sk.placeholders, sk.glitch, *out)
	return nil
}

// skipped counts strings left out of the export because they are never
// translated.
type skipped struct{ placeholders, glitch int }

func exportRows(f *wolvenkit.File, sk *skipped) (rows []crowdincsv.Row, dups []string) {
	seen := map[string]bool{}
	for _, e := range f.Entries {
		gendered := e.Female != "" && e.Male != ""
		for _, male := range []bool{false, true} {
			text := e.Female
			if male {
				text = e.Male
			}
			if text == "" {
				continue
			}
			if cyberpunk2077.IsVOPlaceholder(text) {
				sk.placeholders++
				continue
			}
			if cyberpunk2077.IsGlitchText(text) {
				sk.glitch++
				continue
			}
			key := e.Key(male)
			if seen[key] {
				dups = append(dups, key)
				continue
			}
			seen[key] = true
			rows = append(rows, crowdincsv.Row{ID: key, Source: text, Context: context(e, gendered, male)})
		}
	}
	return rows, dups
}

func context(e wolvenkit.Entry, gendered, male bool) string {
	var parts []string
	if e.SecondaryKey != "" {
		parts = append(parts, e.SecondaryKey)
	}
	switch {
	case gendered && male:
		parts = append(parts, "male V variant")
	case gendered:
		parts = append(parts, "female V variant")
	}
	return strings.Join(parts, " | ")
}

// --- import ------------------------------------------------------------------

func cmdImport(args []string) error {
	fl := flag.NewFlagSet("import", flag.ExitOnError)
	tmpl := fl.String("template", "", "original WolvenKit JSON tree (required)")
	in := fl.String("in", "", "translated CSV or XLIFF tree (required)")
	out := fl.String("out", "", "JSON tree to write (required)")
	required(fl, args, "template", "in", "out")

	cache := map[string]map[string]string{} // base rel -> id -> translation
	var files, copied, replaced, kept, unknown, rejected int

	err := walkFiles(*tmpl, func(rel string) error {
		src := filepath.Join(*tmpl, rel)
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dst := filepath.Join(*out, rel)
		if !strings.HasSuffix(rel, jsonExt) {
			copied++
			return writeFile(dst, b)
		}

		base := baseRel(rel)
		tr, ok := cache[base]
		if !ok {
			var bad int
			tr, bad, err = loadTranslations(filepath.Join(*in, base))
			if err != nil {
				return err
			}
			rejected += bad
			cache[base] = tr
		}
		if tr == nil {
			copied++
			return writeFile(dst, b)
		}

		f, err := wolvenkit.Parse(b)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		nb, st, err := wolvenkit.Splice(b, f, tr)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if n := countUnknown(f, tr); n > 0 {
			unknown += n
			fmt.Fprintf(os.Stderr, "warn: %s: %d translated ids not in the template\n", rel, n)
		}
		files++
		replaced += st.Replaced
		kept += st.Kept
		return writeFile(dst, nb)
	})
	if err != nil {
		return err
	}
	fmt.Printf("imported %d files (%d strings translated, %d kept as source, %d unknown ids, %d rejected for broken tags); %d copied unchanged -> %s\n",
		files, replaced, kept, unknown, rejected, copied, *out)
	return nil
}

// loadTranslations returns id -> non-empty translation from base+".xlf" or
// base+".csv", or nil if neither exists. rejected counts XLIFF units whose
// tags were broken; they keep the source text.
func loadTranslations(base string) (tr map[string]string, rejected int, err error) {
	xlf, csvPath := base+".xlf", base+".csv"
	hasX, hasC := exists(xlf), exists(csvPath)
	switch {
	case hasX && hasC:
		return nil, 0, fmt.Errorf("%s: both .xlf and .csv present, keep one", base)
	case hasX:
		return loadXLIFF(xlf)
	case hasC:
		tr, err := loadCSV(csvPath)
		return tr, 0, err
	}
	return nil, 0, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadCSV(path string) (map[string]string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	rows, err := crowdincsv.Read(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	tr := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Translation != "" {
			tr[r.ID] = r.Translation
		}
	}
	return tr, nil
}

func loadXLIFF(path string) (map[string]string, int, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	f, problems, err := xliff.Read(fh)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "warn: %s: unit %s rejected (%s), source text kept\n", path, p.ID, p.Reason)
	}
	tr := make(map[string]string, len(f.Units))
	for _, u := range f.Units {
		if v := xliff.Plain(u.Target); u.Target != nil && v != "" {
			tr[u.ID] = v
		}
	}
	return tr, len(problems), nil
}

func countUnknown(f *wolvenkit.File, tr map[string]string) int {
	known := make(map[string]bool, 2*len(f.Entries))
	for _, e := range f.Entries {
		known[e.Key(false)], known[e.Key(true)] = true, true
	}
	n := 0
	for id := range tr {
		if !known[id] {
			n++
		}
	}
	return n
}

// --- xliff -------------------------------------------------------------------

func cmdXLIFF(args []string) error {
	fl := flag.NewFlagSet("xliff", flag.ExitOnError)
	in := fl.String("in", "", "CSV tree, source only or translated (required)")
	out := fl.String("out", "", "XLIFF tree to write (required)")
	srcLang := fl.String("source-lang", "ru", "source language code")
	tgtLang := fl.String("target-lang", "be", "target language code")
	required(fl, args, "in", "out")

	var files, units, targets, skipped, dropped int
	err := walkFiles(*in, func(rel string) error {
		if !strings.HasSuffix(rel, ".csv") {
			return nil
		}
		fh, err := os.Open(filepath.Join(*in, rel))
		if err != nil {
			return err
		}
		rows, err := crowdincsv.Read(fh)
		fh.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}

		f := &xliff.File{Original: filepath.ToSlash(rel), SourceLang: *srcLang, TargetLang: *tgtLang}
		for _, r := range rows {
			src := cyberpunk2077.Pieces(r.Source)
			if !cyberpunk2077.HasCyrillicText(src) {
				skipped++
				continue
			}
			u := xliff.Unit{ID: r.ID, Note: r.Context, Source: toXLIFF(src)}
			if r.Translation != "" {
				u.Target = toXLIFF(cyberpunk2077.Pieces(r.Translation))
				u.State = "needs-review-translation"
				targets++
			}
			f.Units = append(f.Units, u)
		}
		if len(f.Units) == 0 {
			return nil
		}

		var buf bytes.Buffer
		lost, err := xliff.Write(&buf, f)
		if err != nil {
			return err
		}
		if len(lost) > 0 {
			fmt.Fprintf(os.Stderr, "warn: %s: tags of %d translations do not match the source, written without target: %v\n", rel, len(lost), lost)
		}
		files++
		units += len(f.Units)
		dropped += len(lost)
		return writeFile(filepath.Join(*out, strings.TrimSuffix(rel, ".csv")+".xlf"), buf.Bytes())
	})
	if err != nil {
		return err
	}
	fmt.Printf("wrote %d units (%d with a translation to review, %d dropped for tag mismatch) into %d files; %d rows without Cyrillic text skipped -> %s\n",
		units, targets-dropped, dropped, files, skipped, *out)
	return nil
}

func toXLIFF(ps []cyberpunk2077.Piece) []xliff.Piece {
	out := make([]xliff.Piece, len(ps))
	for i, p := range ps {
		out[i] = xliff.Piece{Text: p.Text, Code: p.Code}
	}
	return out
}

// --- ref ---------------------------------------------------------------------

func cmdRef(args []string) error {
	fl := flag.NewFlagSet("ref", flag.ExitOnError)
	in := fl.String("in", "", "WolvenKit JSON tree holding several language folders (required)")
	out := fl.String("out", "", "XLIFF tree to write (required)")
	srcDir := fl.String("source-dir", "ru-ru", "language folder of the source text")
	refDir := fl.String("ref-lang-dir", "", "language folder of the reference translation, e.g. en-us (required)")
	srcLang := fl.String("source-lang", "ru", "source language code")
	tgtLang := fl.String("target-lang", "en", "target language code")
	required(fl, args, "in", "out", "ref-lang-dir")

	var files, units, targets, loose, noRef, missing int
	var sk skipped
	err := walkFiles(*in, func(rel string) error {
		refRel, ok := swapSegment(rel, *srcDir, *refDir)
		if !ok || !strings.HasSuffix(rel, jsonExt) || isFinal(rel) {
			return nil
		}
		f, err := parseFile(filepath.Join(*in, rel))
		if err != nil {
			return err
		}
		ref := map[string]string{}
		switch rf, err := parseFile(filepath.Join(*in, refRel)); {
		case errors.Is(err, fs.ErrNotExist):
			missing++
		case err != nil:
			return err
		default:
			for _, e := range rf.Entries {
				for _, male := range []bool{false, true} {
					text := e.Female
					if male && e.Male != "" {
						// Otherwise the one ungendered text serves both V variants.
						text = e.Male
					}
					if text != "" && !cyberpunk2077.IsVOPlaceholder(text) && !cyberpunk2077.IsGlitchText(text) {
						ref[e.Key(male)] = text
					}
				}
			}
		}

		rows, _ := exportRows(f, &sk)
		x := &xliff.File{Original: filepath.ToSlash(csvRel(rel)), SourceLang: *srcLang, TargetLang: *tgtLang, LooseTargetCodes: true}
		for _, r := range rows {
			src := cyberpunk2077.Pieces(r.Source)
			if !cyberpunk2077.HasCyrillicText(src) {
				continue
			}
			u := xliff.Unit{ID: r.ID, Note: r.Context, Source: toXLIFF(src)}
			if text, ok := ref[r.ID]; ok {
				u.Target = toXLIFF(cyberpunk2077.Pieces(text))
				u.State = "final"
				targets++
				if !sameCodes(u.Source, u.Target) {
					loose++
				}
			} else {
				noRef++
			}
			x.Units = append(x.Units, u)
		}
		if len(x.Units) == 0 {
			return nil
		}

		var buf bytes.Buffer
		if _, err := xliff.Write(&buf, x); err != nil {
			return err
		}
		files++
		units += len(x.Units)
		return writeFile(filepath.Join(*out, baseRel(rel)+".xlf"), buf.Bytes())
	})
	if err != nil {
		return err
	}
	fmt.Printf("wrote %d units into %d files: %d with a %s reference (%d with tags differing from the source), %d without one; %d reference files missing -> %s\n",
		units, files, targets, *refDir, loose, noRef, missing, *out)
	return nil
}

func parseFile(path string) (*wolvenkit.File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := wolvenkit.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// swapSegment replaces the path segment from with to; ok is false if rel has
// no such segment.
func swapSegment(rel, from, to string) (string, bool) {
	parts := strings.Split(rel, string(filepath.Separator))
	for i, p := range parts {
		if p == from {
			parts[i] = to
			return filepath.Join(parts...), true
		}
	}
	return "", false
}

// sameCodes reports whether both segments carry the same codes, in any order.
func sameCodes(a, b []xliff.Piece) bool {
	count := map[string]int{}
	for _, p := range a {
		if p.Code {
			count[p.Text]++
		}
	}
	for _, p := range b {
		if p.Code {
			count[p.Text]--
		}
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}

// --- verify ------------------------------------------------------------------

func cmdVerify(args []string) error {
	fl := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fl.String("in", "", "WolvenKit JSON tree (required)")
	required(fl, args, "in")

	var files, strs int
	var bad []string
	err := walkFiles(*in, func(rel string) error {
		if !strings.HasSuffix(rel, jsonExt) {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(*in, rel))
		if err != nil {
			return err
		}
		f, err := wolvenkit.Parse(b)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", rel, err))
			return nil
		}
		self := map[string]string{}
		for _, e := range f.Entries {
			self[e.Key(false)], self[e.Key(true)] = e.Female, e.Male
		}
		nb, st, err := wolvenkit.Splice(b, f, self)
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("%s: %v", rel, err))
		case !bytes.Equal(nb, b):
			bad = append(bad, rel+": re-encoded bytes differ")
		}
		files++
		strs += st.Replaced
		return nil
	})
	if err != nil {
		return err
	}
	for _, s := range bad {
		fmt.Fprintln(os.Stderr, "FAIL", s)
	}
	fmt.Printf("verified %d files, %d strings re-encoded, %d failures\n", files, strs, len(bad))
	if len(bad) > 0 {
		return fmt.Errorf("verify: %d files do not round-trip", len(bad))
	}
	return nil
}
