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
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// carryStats counts what happened to the units of one XLIFF file (or, summed,
// of the whole tree) when moving to a new version of the source text.
type carryStats struct {
	Same        int // source unchanged, translation carried over
	SamePending int // source unchanged, but it had no translation before
	Broken      int // source unchanged, but the translation lost its markup: translate again
	Changed     int // source text differs from the previous version
	Added       int // id not in the previous version
	Removed     int // previous id that no longer exists
}

func (s *carryStats) add(o carryStats) {
	s.Same += o.Same
	s.SamePending += o.SamePending
	s.Broken += o.Broken
	s.Changed += o.Changed
	s.Added += o.Added
	s.Removed += o.Removed
}

// carry returns src with the translations of prev copied onto every unit whose
// source text is unchanged, keeping the previous state (translated, ...). A
// unit whose source changed gets its old target flagged needs-adaptation when
// keepChanged is set, and none otherwise. need lists, in order, the ids that
// still have no target and so need a fresh translation. The source side is
// always taken from src.
func carry(src, prev []xliff.Unit, keepChanged bool) (out []xliff.Unit, need []string, st carryStats) {
	old := make(map[string]*xliff.Unit, len(prev))
	for i := range prev {
		old[prev[i].ID] = &prev[i]
	}
	seen := make(map[string]bool, len(src))
	out = make([]xliff.Unit, len(src))
	for i, u := range src {
		seen[u.ID] = true
		u.Target, u.State = nil, ""
		p := old[u.ID]
		switch {
		case p == nil:
			st.Added++
		case xliff.Plain(p.Source) != xliff.Plain(u.Source):
			st.Changed++
			if keepChanged && p.Target != nil {
				u.Target, u.State = p.Target, "needs-adaptation"
			}
		case p.Target == nil:
			st.SamePending++
		default:
			u.Target, u.State = p.Target, p.State
			if !sameCodes(p.Source, u.Source) {
				u.Target = retag(p.Target, u.Source)
			}
			if sameCodes(u.Source, u.Target) {
				st.Same++
			} else {
				st.Broken++
				u.Target, u.State = nil, ""
			}
		}
		if u.Target == nil {
			need = append(need, u.ID)
		}
		out[i] = u
	}
	for _, p := range prev {
		if !seen[p.ID] {
			st.Removed++
		}
	}
	return out, need, st
}

// retag re-splits a target into text and codes the way the analyzer now splits
// source markup, for a translation made when some markup was still plain text.
// It returns tgt unchanged when the codes still do not match src.
func retag(tgt, src []xliff.Piece) []xliff.Piece {
	if tgt == nil {
		return nil
	}
	if t := toXLIFF(cyberpunk2077.Pieces(xliff.Plain(tgt))); sameCodes(src, t) {
		return t
	}
	return tgt
}

// applyFill gives every unit that has no target the machine translation in tr,
// marked needs-review-translation, and returns how many it filled.
func applyFill(units []xliff.Unit, tr map[string]string) int {
	n := 0
	for i := range units {
		u := &units[i]
		if text := tr[u.ID]; u.Target == nil && text != "" {
			u.Target = toXLIFF(cyberpunk2077.Pieces(text))
			u.State = "needs-review-translation"
			n++
		}
	}
	return n
}

func cmdUpdate(args []string) error {
	fl := flag.NewFlagSet("update", flag.ExitOnError)
	in := fl.String("in", "", "WolvenKit JSON tree holding the new source text (required)")
	prev := fl.String("prev", "", "XLIFF tree of the previous version whose translations to carry over (required)")
	out := fl.String("out", "", "XLIFF tree to write (required)")
	srcDir := fl.String("source-dir", "ru-ru", "language folder of the source text")
	srcLang := fl.String("source-lang", "ru", "source language code")
	tgtLang := fl.String("target-lang", "be", "target language code")
	changed := fl.String("changed", "drop", "units whose source changed: drop the old translation, or keep it as needs-adaptation")
	delta := fl.String("delta", "", "CSV tree to write the units that still need a translation into")
	fill := fl.String("fill", "", "CSV tree of machine translations (from the -delta run) for units still without one")
	extra := fl.String("extra-from", "", "language folder (e.g. cz-cz) whose real text makes VO-placeholder-only source strings units too")
	required(fl, args, "in", "prev", "out")
	if *changed != "drop" && *changed != "keep" {
		return fmt.Errorf("update: -changed must be drop or keep, got %q", *changed)
	}

	var files, units, need, filled, dropped, rejected int
	var st carryStats
	var sk skipped
	done := map[string]bool{}
	err := walkFiles(*in, func(rel string) error {
		if _, ok := swapSegment(rel, *srcDir, *srcDir); !ok || !strings.HasSuffix(rel, jsonExt) || isFinal(rel) {
			return nil
		}
		f, err := parseFile(filepath.Join(*in, rel))
		if err != nil {
			return err
		}
		ex, err := extrasFor(*in, rel, *srcDir, *extra, f)
		if err != nil {
			return err
		}
		rows, _ := exportRowsWith(f, &sk, ex)
		byID := make(map[string]crowdincsv.Row, len(rows))
		var src []xliff.Unit
		for _, r := range rows {
			ps := cyberpunk2077.Pieces(r.Source)
			if _, isExtra := ex[r.ID]; !isExtra && !cyberpunk2077.HasCyrillicText(ps) {
				continue
			}
			byID[r.ID] = r
			src = append(src, xliff.Unit{ID: r.ID, Note: r.Context, Source: toXLIFF(ps)})
		}
		if len(src) == 0 {
			return nil
		}

		base := baseRel(rel)
		done[base] = true
		old, bad, err := readXLIFFUnits(filepath.Join(*prev, base+".xlf"))
		if err != nil {
			return err
		}
		rejected += bad
		us, ids, s := carry(src, old, *changed == "keep")
		st.add(s)
		if *fill != "" {
			tr, err := loadCSVIfExists(filepath.Join(*fill, base+".csv"))
			if err != nil {
				return err
			}
			filled += applyFill(us, tr)
		}

		if *delta != "" {
			var rest []crowdincsv.Row
			for _, u := range us {
				if u.Target == nil {
					rest = append(rest, byID[u.ID])
				}
			}
			if len(rest) > 0 {
				var buf bytes.Buffer
				if err := crowdincsv.Write(&buf, rest); err != nil {
					return err
				}
				if err := writeFile(filepath.Join(*delta, base+".csv"), buf.Bytes()); err != nil {
					return err
				}
			}
		}
		need += len(ids)

		x := &xliff.File{Original: filepath.ToSlash(csvRel(rel)), SourceLang: *srcLang, TargetLang: *tgtLang, Units: us}
		var buf bytes.Buffer
		lost, err := xliff.Write(&buf, x)
		if err != nil {
			return err
		}
		if len(lost) > 0 {
			fmt.Fprintf(os.Stderr, "warn: %s: %d translations have tags that do not match the source, written without target: %v\n", rel, len(lost), lost)
			dropped += len(lost)
		}
		files++
		units += len(us)
		return writeFile(filepath.Join(*out, base+".xlf"), buf.Bytes())
	})
	if err != nil {
		return err
	}

	// Files of the previous version with no counterpart in the new source.
	var goneFiles, goneUnits int
	err = walkFiles(*prev, func(rel string) error {
		base := strings.TrimSuffix(rel, ".xlf")
		if base == rel || done[base] {
			return nil
		}
		us, _, err := readXLIFFUnits(filepath.Join(*prev, rel))
		if err != nil {
			return err
		}
		goneFiles++
		goneUnits += len(us)
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("wrote %d units into %d files -> %s\n", units, files, *out)
	fmt.Printf("  carried over: %d translations (%d rejected for broken tags in %s)\n", st.Same, rejected, *prev)
	fmt.Printf("  source changed: %d, new: %d, were untranslated: %d, translation lost its markup: %d\n", st.Changed, st.Added, st.SamePending, st.Broken)
	fmt.Printf("  removed: %d units, plus %d units in %d files that no longer exist\n", st.Removed, goneUnits, goneFiles)
	fmt.Printf("  still without a translation: %d (machine translations filled in: %d, dropped for tag mismatch: %d)\n", need-filled, filled, dropped)
	return nil
}

// readXLIFFUnits reads the units of an XLIFF file, or nil if it does not exist.
// bad counts units whose target was rejected for broken tags.
func readXLIFFUnits(path string) (us []xliff.Unit, bad int, err error) {
	fh, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	f, problems, err := xliff.Read(fh)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	return f.Units, len(problems), nil
}

// loadCSVIfExists is loadCSV that treats a missing file as no translations.
func loadCSVIfExists(path string) (map[string]string, error) {
	if !exists(path) {
		return nil, nil
	}
	return loadCSV(path)
}
