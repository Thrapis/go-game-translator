package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	lingvanexsrv "github.com/Thrapis/go-game-translator/internal/backend/lingvanex"
	"github.com/Thrapis/go-game-translator/internal/config"
	"github.com/Thrapis/go-game-translator/internal/markup"
	"github.com/Thrapis/go-game-translator/internal/translate"
	_ "github.com/Thrapis/go-game-translator/internal/translate/lingvanex"
	"github.com/Thrapis/go-game-translator/internal/xliff"
)

var sentinelRe = regexp.MustCompile(`§(\d+)§`)

// updateStats counts what xliff-update did with each unit of the new file.
type updateStats struct {
	Carried     int // translation kept: source unchanged
	Retranslate int // machine-translated: new unit or changed source
	Dropped     int // units in the previous file that are gone from the new one
}

// letters reduces text to its lowercase letters and digits, so that a source
// that only gained or lost punctuation or spacing counts as unchanged.
func letters(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func codeTexts(ps []xliff.Piece) []string {
	var out []string
	for _, p := range ps {
		if p.Code {
			out = append(out, p.Text)
		}
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reusable reports whether the translation of prev still fits the new unit:
// it has a target, the markup is the same and the source only differs in
// punctuation or spacing (or not at all).
func reusable(prev, cur xliff.Unit) bool {
	if prev.Target == nil || !sameStrings(codeTexts(prev.Source), codeTexts(cur.Source)) {
		return false
	}
	return letters(xliff.Plain(prev.Source)) == letters(xliff.Plain(cur.Source))
}

// updateUnits builds the new translation file: the previous translation for
// every unit that is still valid, a machine translation (via t, in state
// state) for the rest. cur supplies the unit list, order, notes and header.
func updateUnits(ctx context.Context, t translate.Translator, prev, cur *xliff.File, state string, batch int) (*xliff.File, updateStats, error) {
	out, todo, st := planUpdate(prev, cur)
	if len(todo) == 0 {
		return out, st, nil
	}
	srcs := make([][]xliff.Piece, len(todo))
	for i, idx := range todo {
		srcs[i] = out.Units[idx].Source
	}
	targets, err := translatePieces(ctx, t, srcs, out.SourceLang, out.TargetLang, batch)
	if err != nil {
		return nil, st, err
	}
	for i, idx := range todo {
		out.Units[idx].Target, out.Units[idx].State = targets[i], state
		st.Retranslate++
	}
	return out, st, nil
}

// planUpdate carries over every still-valid translation and lists (by index
// into the result) the units that need a new one.
func planUpdate(prev, cur *xliff.File) (out *xliff.File, todo []int, st updateStats) {
	byID := make(map[string]xliff.Unit, len(prev.Units))
	for _, u := range prev.Units {
		byID[u.ID] = u
	}
	out = &xliff.File{Original: cur.Original, SourceLang: cur.SourceLang, TargetLang: cur.TargetLang, LooseTargetCodes: cur.LooseTargetCodes}
	seen := map[string]bool{}
	for _, u := range cur.Units {
		seen[u.ID] = true
		nu := xliff.Unit{ID: u.ID, Note: u.Note, Source: u.Source}
		if p, ok := byID[u.ID]; ok && reusable(p, u) {
			nu.Target, nu.State = p.Target, p.State
			st.Carried++
		} else {
			todo = append(todo, len(out.Units))
		}
		out.Units = append(out.Units, nu)
	}
	for _, u := range prev.Units {
		if !seen[u.ID] {
			st.Dropped++
		}
	}
	return out, todo, st
}

// maskPieces turns a segmented source into text with §N§ sentinels for every
// code, returning the markers in sentinel order.
func maskPieces(ps []xliff.Piece) (string, []string) {
	var b strings.Builder
	var markers []string
	for _, p := range ps {
		if p.Code {
			fmt.Fprintf(&b, "§%d§", len(markers))
			markers = append(markers, p.Text)
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String(), markers
}

// unmaskPieces is the inverse: text with sentinels back to pieces.
func unmaskPieces(s string, markers []string) []xliff.Piece {
	var out []xliff.Piece
	last := 0
	for _, m := range sentinelRe.FindAllStringSubmatchIndex(s, -1) {
		i, _ := strconv.Atoi(s[m[2]:m[3]])
		if m[0] > last {
			out = append(out, xliff.Piece{Text: s[last:m[0]]})
		}
		out = append(out, xliff.Piece{Text: markers[i], Code: true})
		last = m[1]
	}
	if last < len(s) {
		out = append(out, xliff.Piece{Text: s[last:]})
	}
	return out
}

// firstLetter returns the first letter of s outside sentinels, or 0.
func firstLetter(s string) rune {
	for _, r := range sentinelRe.ReplaceAllString(s, "\x00") {
		if unicode.IsLetter(r) {
			return r
		}
	}
	return 0
}

// capitalize upper-cases the first letter of s that is not inside a sentinel.
func capitalize(s string) string {
	for i := 0; i < len(s); {
		if loc := sentinelRe.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsLetter(r) {
			return s[:i] + string(unicode.ToUpper(r)) + s[i+size:]
		}
		i += size
	}
	return s
}

// polish restores what the model drops: surrounding spaces and the case of the
// first letter.
func polish(src, tr string) string {
	lead := len(src) - len(strings.TrimLeft(src, " "))
	trail := len(src) - len(strings.TrimRight(src, " "))
	out := strings.Repeat(" ", lead) + strings.TrimSpace(tr) + strings.Repeat(" ", trail)
	if r := firstLetter(src); r != 0 && unicode.IsUpper(r) && unicode.IsLower(firstLetter(out)) {
		out = capitalize(out)
	}
	return out
}

// translatePieces machine-translates sources that may contain codes and line
// breaks. Codes travel as §N§ sentinels, each line is its own batch item (the
// model has a decoding limit and the client flattens newlines). A string whose
// sentinels come back damaged is retranslated piece by piece.
func translatePieces(ctx context.Context, t translate.Translator, srcs [][]xliff.Piece, from, to string, batch int) ([][]xliff.Piece, error) {
	type job struct{ unit, seg int }
	segs := make([][]string, len(srcs))
	markers := make([][]string, len(srcs))
	var texts []string
	var jobs []job
	for i, ps := range srcs {
		masked, m := maskPieces(ps)
		markers[i] = m
		segs[i] = strings.Split(masked, "\n")
		for j, s := range segs[i] {
			if firstLetter(s) == 0 && !strings.ContainsAny(s, "0123456789") {
				continue // nothing to translate: spaces, punctuation, bare sentinels
			}
			texts = append(texts, s)
			jobs = append(jobs, job{i, j})
		}
	}
	trs := make([]string, 0, len(texts))
	for lo := 0; lo < len(texts); lo += batch {
		hi := min(lo+batch, len(texts))
		chunk := append([]string(nil), texts[lo:hi]...)
		got, err := translate.Batch(ctx, t, chunk, from, to)
		if err != nil { // retry the chunk one by one before giving up
			got = got[:0]
			for _, s := range texts[lo:hi] {
				v, err := t.Translate(ctx, s, from, to)
				if err != nil {
					return nil, err
				}
				got = append(got, v)
			}
		}
		trs = append(trs, got...)
	}
	out := make([][]xliff.Piece, len(srcs))
	done := make([][]string, len(srcs))
	for i := range segs {
		done[i] = append([]string(nil), segs[i]...)
	}
	for k, j := range jobs {
		done[j.unit][j.seg] = polish(segs[j.unit][j.seg], trs[k])
	}
	for i := range srcs {
		joined := strings.Join(done[i], "\n")
		if markup.SentinelsIntact(joined, len(markers[i])) || len(markers[i]) == 0 && !strings.Contains(joined, "§") {
			out[i] = unmaskPieces(joined, markers[i])
			continue
		}
		// Damaged sentinels: translate the text pieces on their own.
		pieces := make([]xliff.Piece, len(srcs[i]))
		for k, p := range srcs[i] {
			pieces[k] = p
			if p.Code || strings.TrimSpace(p.Text) == "" {
				continue
			}
			v, err := t.Translate(ctx, strings.ReplaceAll(p.Text, "\n", " "), from, to)
			if err != nil {
				return nil, err
			}
			pieces[k].Text = polish(p.Text, v)
		}
		out[i] = pieces
	}
	return out, nil
}

// readXLIFF reads one XLIFF file and warns about targets that were dropped
// for mismatching markup.
func readXLIFF(path string) (*xliff.File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, problems, err := xliff.Read(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "warn: %s: %s: target ignored: %s\n", filepath.Base(path), p.ID, p.Reason)
	}
	return f, nil
}

// runXLIFFUpdate brings a translated XLIFF up to date with a freshly exported
// one (a new game version): translations whose source did not change are kept
// as they are, new and changed units are machine-translated with the local
// Lingvanex server and marked for review.
func runXLIFFUpdate(args []string) error {
	fl := flag.NewFlagSet("xliff-update", flag.ExitOnError)
	prevPath := fl.String("prev", "", "current translated XLIFF (required)")
	newPath := fl.String("new", "", "freshly exported XLIFF of the new game version, source only (required)")
	out := fl.String("out", "", "output .xlf file (required; may be the same as -prev)")
	state := fl.String("state", "needs-review-translation", "target state of machine-translated units")
	dir := fl.String("server-dir", "third_party/lingvanex-server", "folder of the Lingvanex server (run from the repo root)")
	port := fl.Int("port", 8000, "Lingvanex server port")
	batch := fl.Int("batch", 40, "lines per translation request")
	dry := fl.Bool("dry-run", false, "only count what would be carried over and retranslated")
	fl.Parse(args)
	if *prevPath == "" || *newPath == "" || *out == "" {
		fl.Usage()
		os.Exit(2)
	}
	prev, err := readXLIFF(*prevPath)
	if err != nil {
		return err
	}
	cur, err := readXLIFF(*newPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var tr translate.Translator
	if !*dry {
		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		cfg := config.Lingvanex{
			Manage: true, Address: "127.0.0.1", Port: *port, Workdir: *dir,
			Command:       []string{"py", "./server.py"},
			HealthTimeout: config.Duration(2 * time.Minute), StopTimeout: config.Duration(10 * time.Second),
			RequestTimeout: config.Duration(5 * time.Minute),
		}
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Address, strconv.Itoa(*port)), time.Second); err == nil {
			conn.Close()
			cfg.Manage = false // a server is already running; use it
		}
		srv := lingvanexsrv.New(cfg, log)
		if err := srv.Start(ctx); err != nil {
			return err
		}
		defer srv.Stop()
		tr, err = translate.Build([]string{"lingvanex"}, translate.Options{LingvanexAddress: cfg.Address, LingvanexPort: *port, RequestTimeout: 5 * time.Minute}, log)
		if err != nil {
			return err
		}
	}

	if *dry {
		_, todo, st := planUpdate(prev, cur)
		fmt.Printf("%s: %d units; %d translations kept, %d to translate, %d gone from the game\n",
			filepath.Base(*newPath), len(cur.Units), st.Carried, len(todo), st.Dropped)
		return nil
	}
	res, st, err := updateUnits(ctx, tr, prev, cur, *state, *batch)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d units; %d translations kept, %d machine-translated, %d gone from the game\n",
		filepath.Base(*newPath), len(cur.Units), st.Carried, st.Retranslate, st.Dropped)
	var buf bytes.Buffer
	dropped, err := xliff.Write(&buf, res)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "warn: tags of %d translations do not match the source, written without target: %v\n", len(dropped), dropped)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("machine-translated %d units (state %q) -> %s\n", st.Retranslate, *state, *out)
	return nil
}
