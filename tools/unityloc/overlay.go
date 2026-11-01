package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Thrapis/go-game-translator/internal/xliff"
)

// overlayTargets reads the translated targets of an XLIFF file in unit order,
// skipping units without a translation.
func overlayTargets(path string) (ids, texts []string, err error) {
	f, err := readXLIFF(path)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, u := range f.Units {
		t := xliff.Plain(u.Target)
		if u.Target == nil || strings.TrimSpace(t) == "" {
			continue
		}
		if seen[u.ID] {
			return nil, nil, fmt.Errorf("%s: duplicate unit id %s", filepath.Base(path), u.ID)
		}
		seen[u.ID] = true
		ids, texts = append(ids, u.ID), append(texts, t)
	}
	return ids, texts, nil
}

// writeOverlay renders {"ui": {id: text, ...}, "dialogue": {id: text, ...}}
// keeping the order of the lists, one entry per line so diffs stay readable.
func writeOverlay(ui, dialogue [2][]string) ([]byte, error) {
	var buf bytes.Buffer
	section := func(name string, ids, texts []string, last bool) error {
		fmt.Fprintf(&buf, "  %q: {\n", name)
		for i, id := range ids {
			k, err := marshalNoEscape(id)
			if err != nil {
				return err
			}
			v, err := marshalNoEscape(texts[i])
			if err != nil {
				return err
			}
			sep := ","
			if i == len(ids)-1 {
				sep = ""
			}
			fmt.Fprintf(&buf, "    %s: %s%s\n", k, v, sep)
		}
		buf.WriteString("  }")
		if !last {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
		return nil
	}
	buf.WriteString("{\n")
	if err := section("ui", ui[0], ui[1], false); err != nil {
		return nil, err
	}
	if err := section("dialogue", dialogue[0], dialogue[1], true); err != nil {
		return nil, err
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

// runOverlay turns the translated XLIFF files into the single translations
// file the BepInEx plugin reads at run time. Only translated units are
// included, so everything else keeps the game's own text.
func runOverlay(args []string) error {
	fl := flag.NewFlagSet("overlay", flag.ExitOnError)
	uiPath := fl.String("ui", "", "translated Localization.xlf (UI, items, emails)")
	dlgPath := fl.String("dialogue", "", "translated RestoryStory.xlf (dialogue)")
	out := fl.String("out", "", "output translations.json (required)")
	fl.Parse(args)
	if *out == "" || (*uiPath == "" && *dlgPath == "") {
		fl.Usage()
		os.Exit(2)
	}
	var ui, dlg [2][]string
	var err error
	if *uiPath != "" {
		if ui[0], ui[1], err = overlayTargets(*uiPath); err != nil {
			return err
		}
	}
	if *dlgPath != "" {
		if dlg[0], dlg[1], err = overlayTargets(*dlgPath); err != nil {
			return err
		}
	}
	data, err := writeOverlay(ui, dlg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d UI and %d dialogue translations -> %s\n", len(ui[0]), len(dlg[0]), *out)
	return nil
}
