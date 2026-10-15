package tcoaaltxt

import (
	"strings"
	"testing"

	"github.com/Thrapis/go-game-translator/internal/extract"
)

const fixture = "[VERSION]\r\n" +
	"3.0.13 : deadbeef\r\n" +
	"\r\n" +
	"[LANGUAGE]\r\n" +
	"English\r\n" +
	"\r\n" +
	"[LABELS]\r\n" +
	"Game : The Coffin\r\n" +
	"\r\n" +
	"[MENUS]\r\n" +
	"New Game    : New Game\r\n" +
	"\r\n" +
	"[SPEAKERS]\r\n" +
	"#id1 : Ashley\r\n" +
	"\r\n" +
	"[DESCRIPTIONS]\r\n" +
	"\r\n" +
	"#k1 (Axe)\r\n" +
	": Chop chop\r\n" +
	"\r\n" +
	"#k2 (Cap)\r\n" +
	": \r\n" +
	"\r\n" +
	"[Map001.json]\r\n" +
	"\r\n" +
	"#a1 (Narrator)\r\n" +
	": Who wouldn't love \r\n" +
	": being stuck at home?\r\n" +
	"\r\n" +
	"#a2 (Andrew)\r\n" +
	": \"Ah.\"\r\n" +
	"\r\n" +
	"[CHOICES]\r\n" +
	"#c1 : Call again.\r\n" +
	"\r\n"

func TestExtractSlots(t *testing.T) {
	f := format{}
	lines, s, err := f.Extract(strings.NewReader(fixture), "")
	if err != nil {
		t.Fatal(err)
	}
	// LANGUAGE, Game, New Game, Ashley, "Chop chop", "" (k2), 2×Map001 a1,
	// a2 "Ah.", c1 => 10
	if len(lines) != 10 {
		t.Fatalf("got %d slots, want 10: %+v", len(lines), lines)
	}
	if lines[0].Value != "English" || lines[0].Tag != "LANGUAGE" {
		t.Errorf("LANGUAGE slot = %+v", lines[0])
	}
	if lines[1].Value != "The Coffin" || lines[1].Tag != "LABELS/Game" {
		t.Errorf("LABELS slot = %+v", lines[1])
	}
	if lines[2].Value != "New Game" || lines[2].Tag != "MENUS/New Game" {
		t.Errorf("MENUS slot = %+v", lines[2])
	}
	if lines[3].Value != "Ashley" || lines[3].Tag != "#id1" {
		t.Errorf("SPEAKERS slot = %+v", lines[3])
	}
	if lines[4].Value != "Chop chop" || lines[4].Tag != "#k1" {
		t.Errorf("DESCRIPTIONS slot = %+v", lines[4])
	}
	// Map001 a1 has two value lines sharing the tag
	if lines[6].Tag != "#a1" || lines[7].Tag != "#a1" {
		t.Errorf("replica tags = %q,%q want #a1,#a1", lines[6].Tag, lines[7].Tag)
	}
	if lines[6].Value != "Who wouldn't love " {
		t.Errorf("value keeps trailing space: %q", lines[6].Value)
	}
	if lines[9].Value != "Call again." || lines[9].Tag != "#c1" {
		t.Errorf("CHOICES slot = %+v", lines[9])
	}
	_ = s
}

func TestComposeRoundTrip(t *testing.T) {
	f := format{}
	lines, s, err := f.Extract(strings.NewReader(fixture), "")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := f.Compose(&b, lines, s, ""); err != nil {
		t.Fatal(err)
	}
	if b.String() != fixture {
		t.Errorf("round trip differs:\n got: %q\nwant: %q", b.String(), fixture)
	}
}

func TestComposeSubstitutes(t *testing.T) {
	f := format{}
	lines, s, err := f.Extract(strings.NewReader(fixture), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range lines {
		if lines[i].Value != "" {
			lines[i].Value = "X"
		}
	}
	var b strings.Builder
	if err := f.Compose(&b, lines, s, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Game : X\r\n",
		"New Game    : X\r\n", // padded key preserved
		"#id1 : X\r\n",
		"#k1 (Axe)\r\n: X\r\n",
		"#c1 : X\r\n",
		"[LANGUAGE]\r\nX\r\n",   // LANGUAGE is a slot now
		"3.0.13 : deadbeef\r\n", // VERSION still untouched
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Who wouldn't love") {
		t.Error("dialogue value not substituted")
	}
}

func TestRegistered(t *testing.T) {
	if _, err := extract.Get("tcoaal-txt"); err != nil {
		t.Fatal(err)
	}
}

func TestEntriesExposeSectionHeaderAndValue(t *testing.T) {
	d, _, err := ParseDoc(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	es := d.Entries()
	byTag := map[string]Entry{}
	for i, e := range es {
		if e.Index != i {
			t.Errorf("entry %d has Index %d", i, e.Index)
		}
		byTag[e.Tag] = e
	}
	if e := byTag["#a2"]; e.Section != "Map001.json" || e.Header != "#a2 (Andrew)" || e.Value != `"Ah."` {
		t.Errorf("#a2 = %+v", e)
	}
	if e := byTag["MENUS/New Game"]; e.Section != "MENUS" || e.Value != "New Game" {
		t.Errorf("MENUS entry = %+v", e)
	}
}
