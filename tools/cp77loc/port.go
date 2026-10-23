package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/game/cyberpunk2077"
	"github.com/Thrapis/go-game-translator/internal/wolvenkit"
)

// portResult is the outcome of porting the strings of one resource onto the
// same resource of another language.
type portResult struct {
	// Tr maps template variant keys to the text to write (see wolvenkit.Splice).
	Tr map[string]string
	// Blank holds male variant keys to empty. The game shows the female text
	// to a male V when the male variant is empty, so a Czech male text with no
	// distinct source counterpart is removed rather than left or duplicated.
	Blank map[string]bool
	// OnlyTemplate are template entries whose id is missing from the source:
	// they keep their own text.
	OnlyTemplate []wolvenkit.Entry
	// OnlyFrom are source entries whose id is missing from the template: they
	// have nowhere to go.
	OnlyFrom []wolvenkit.Entry
	// Gaps are template variants with real text that the source cannot
	// replace (it is empty, a VO placeholder or glitch text), so they keep
	// their own language: strings missing from the source localization.
	Gaps []portGap
}

// portGap is one variant of a template entry left untranslated, and why.
type portGap struct {
	Entry wolvenkit.Entry
	Male  bool
	Why   string
}

// portEntries matches the entries of from to those of tmpl by id. A template
// variant gets the source text of the same variant; when the template has a
// male variant and the source has no distinct male text, the template's male
// variant is emptied (the game then uses the female text for male V); an empty
// male variant is filled when the source has a distinct male text. Template
// glitch text is a deliberate effect and is never overwritten; a template VO
// placeholder is, when the source has real text. Source text that is empty, a
// VO placeholder or glitch text is never copied: those strings are not
// translatable.
func portEntries(tmpl, from *wolvenkit.File) portResult {
	src := make(map[string]wolvenkit.Entry, len(from.Entries))
	for _, e := range from.Entries {
		if _, dup := src[e.ID]; !dup {
			src[e.ID] = e
		}
	}
	r := portResult{Tr: map[string]string{}, Blank: map[string]bool{}}
	have := make(map[string]bool, len(tmpl.Entries))
	for _, e := range tmpl.Entries {
		if have[e.ID] {
			continue
		}
		have[e.ID] = true
		s, ok := src[e.ID]
		if !ok {
			r.OnlyTemplate = append(r.OnlyTemplate, e)
			continue
		}
		// The female variant is the universal one. A source with text only in
		// its male variant (female empty or a placeholder) still has to fill a
		// template female variant that holds text; an empty one is a male-only
		// line and stays that way.
		fem, fromMale := s.Female, false
		if e.Female != "" && (s.Female == "" || cyberpunk2077.IsVOPlaceholder(s.Female)) && usable(s.Male) != "" {
			fem, fromMale = s.Male, true
		}
		if !cyberpunk2077.IsGlitchText(e.Female) {
			if v := usable(fem); v != "" {
				r.Tr[e.Key(false)] = v
			} else if portable(e.Female) && e.Female != "" {
				r.Gaps = append(r.Gaps, portGap{Entry: e, Why: unusableWhy(fem)})
			}
		}
		if e.HasMale && !cyberpunk2077.IsGlitchText(e.Male) {
			m := usable(s.Male)
			if m == s.Female || fromMale {
				m = "" // not a distinct male text
			}
			switch {
			case m != "":
				r.Tr[e.Key(true)] = m
			case e.Male == "":
				// Already empty: the female text serves male V.
			case usable(fem) != "":
				r.Blank[e.Key(true)] = true
			case portable(e.Male):
				why := unusableWhy(s.Male)
				if s.Male == "" {
					why = unusableWhy(s.Female)
				}
				r.Gaps = append(r.Gaps, portGap{Entry: e, Male: true, Why: why})
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range from.Entries {
		if !have[e.ID] && !seen[e.ID] {
			seen[e.ID] = true
			r.OnlyFrom = append(r.OnlyFrom, e)
		}
	}
	return r
}

// portable reports whether a template text may be overwritten.
func portable(s string) bool {
	return !cyberpunk2077.IsVOPlaceholder(s) && !cyberpunk2077.IsGlitchText(s)
}

// usable returns s, or "" if it must not be copied into another language.
func usable(s string) string {
	if !portable(s) {
		return ""
	}
	return s
}

// unusableWhy names why usable rejected s.
func unusableWhy(s string) string {
	switch {
	case s == "":
		return "source empty"
	case cyberpunk2077.IsVOPlaceholder(s):
		return "source VO placeholder"
	default:
		return "source glitch text"
	}
}

func cmdPort(args []string) error {
	fl := flag.NewFlagSet("port", flag.ExitOnError)
	from := fl.String("from", "", "tree holding the translated resources, e.g. the be-json (required)")
	tmpl := fl.String("template", "", "original tree holding the target language folder, e.g. raw (required)")
	out := fl.String("out", "", "tree to write (required)")
	srcDir := fl.String("source-dir", "ru-ru", "language folder inside -from")
	dstDir := fl.String("target-dir", "cz-cz", "language folder inside -template to port into")
	report := fl.String("report", "", "CSV file listing ids on one side only and strings the source cannot fill")
	refs := fl.String("ref-dirs", "en-us,ua-ua", "language folders inside -template shown in the report as reference texts")
	required(fl, args, "from", "template", "out")
	refDirs := splitList(*refs)

	var files, replaced, blanked, kept, noSource, onlyT, onlyF, gaps int
	var rows [][]string
	done := map[string]bool{}
	err := walkFiles(*tmpl, func(rel string) error {
		srcRel, ok := swapSegment(rel, *dstDir, *srcDir)
		if !ok {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(*tmpl, rel))
		if err != nil {
			return err
		}
		dst := filepath.Join(*out, rel)
		if !strings.HasSuffix(rel, jsonExt) {
			return writeFile(dst, b)
		}
		done[srcRel] = true
		g, err := parseFile(filepath.Join(*from, srcRel))
		if errors.Is(err, fs.ErrNotExist) {
			noSource++
			rows = append(rows, []string{filepath.ToSlash(rel), "", "", "file only in " + *dstDir})
			return writeFile(dst, b)
		}
		if err != nil {
			return err
		}
		f, err := wolvenkit.Parse(b)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		r := portEntries(f, g)
		nb, st, err := wolvenkit.SpliceBlanking(b, f, r.Tr, r.Blank)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		files++
		replaced += st.Replaced
		blanked += st.Blanked
		kept += st.Kept
		texts := []map[string]wolvenkit.Entry{byID(f), byID(g)}
		for _, d := range refDirs {
			var m map[string]wolvenkit.Entry
			if rr, ok := swapSegment(rel, *dstDir, d); ok {
				if rf, err := parseFile(filepath.Join(*tmpl, rr)); err == nil {
					m = byID(rf)
				}
			}
			texts = append(texts, m)
		}
		row := func(id string, male bool, issue string) []string {
			variant := ""
			if male {
				variant = "male"
			}
			out := []string{filepath.ToSlash(rel), id, variant, issue}
			for _, m := range texts {
				e, ok := m[id]
				switch {
				case !ok:
					out = append(out, "")
				case male && e.Male != "":
					out = append(out, e.Male)
				default:
					out = append(out, e.Female)
				}
			}
			return out
		}
		for _, e := range r.OnlyTemplate {
			onlyT++
			rows = append(rows, row(e.ID, false, "only in "+*dstDir))
		}
		for _, e := range r.OnlyFrom {
			onlyF++
			rows = append(rows, row(e.ID, false, "only in "+*srcDir))
		}
		for _, gp := range r.Gaps {
			gaps++
			rows = append(rows, row(gp.Entry.ID, gp.Male, gp.Why))
		}
		return writeFile(dst, nb)
	})
	if err != nil {
		return err
	}

	// Source files that have no counterpart in the template language.
	var orphan int
	err = walkFiles(*from, func(rel string) error {
		if _, ok := swapSegment(rel, *srcDir, *srcDir); ok && strings.HasSuffix(rel, jsonExt) && !done[rel] {
			orphan++
			rows = append(rows, []string{filepath.ToSlash(rel), "", "", "file only in " + *srcDir})
		}
		return nil
	})
	if err != nil {
		return err
	}

	if *report != "" {
		header := append([]string{"file", "id", "variant", "issue", *dstDir, *srcDir}, refDirs...)
		if err := writeReport(*report, header, rows); err != nil {
			return err
		}
	}
	fmt.Printf("ported %d files: %d strings replaced, %d %s male variants removed (female serves male V), %d kept as %s\n", files, replaced, blanked, *dstDir, kept, *dstDir)
	fmt.Printf("  ids only in %s: %d (keep their %s text); ids only in %s: %d (dropped)\n", *dstDir, onlyT, *dstDir, *srcDir, onlyF)
	fmt.Printf("  files only in %s: %d (copied unchanged); files only in %s: %d -> %s\n", *dstDir, noSource, *srcDir, orphan, *out)
	fmt.Printf("  %s strings the %s text cannot fill (empty, VO placeholder or glitch text), so they stay in %s: %d\n", *dstDir, *srcDir, *dstDir, gaps)
	return nil
}

func byID(f *wolvenkit.File) map[string]wolvenkit.Entry {
	m := make(map[string]wolvenkit.Entry, len(f.Entries))
	for _, e := range f.Entries {
		if _, dup := m[e.ID]; !dup {
			m[e.ID] = e
		}
	}
	return m
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeReport(path string, header []string, rows [][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(fh)
	_ = w.Write(header)
	_ = w.WriteAll(rows)
	w.Flush()
	if err := w.Error(); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}
