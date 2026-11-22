package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dshPresetWrite puts s into f, its folder made.
func dshPresetWrite(t *testing.T, f, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dshPresetPatch is a home patch list with a row of the user's, as the
// registry's own README declares them: the registry row first, a preset of
// the user's second.
func dshPresetPatch(t *testing.T, dir string) string {
	t.Helper()
	f := DshPresetFile(dir)
	dshPresetWrite(t, f, strings.Join([]string{
		"# the user's own notes",
		"",
		"- id: agent-preset-registry",
		"  name: '@deepseek-ai/dsh-agent-preset-registry'",
		"  config:",
		"    default: standard",
		"- id: preset-mine",
		"  name: '@deepseek-ai/dsh-agent-preset'",
		"  config:",
		"    id: mine",
		"    description: The user's own preset.",
		"    order: 3",
		"    plugins:",
		"      - name: '@deepseek-ai/dsh-persona'",
		"        config:",
		"          prefix: The user's own prompt.",
		"", // dshParse's list keeps to its entries; the file ends in one newline
	}, "\n"))
	return f
}

func dshPresetSandbox(t *testing.T) string {
	t.Helper()
	home := dshTempHome(t)
	dshVersionIs(t, "0.2.1")
	dshNoApp(t)
	return home
}

func TestDshPresetPutReadDrop(t *testing.T) {
	home := dshPresetSandbox(t)
	dir := filepath.Join(home, ".dsh")
	f := dshPresetPatch(t, dir)
	user := readOf(t, f)

	body := DshPresetBody("reviewer", "Reviews a diff.", "You review code.\n\nReport bugs only.\n")
	if !strings.Contains(string(body), "- id: preset-magpie-reviewer # magpie") {
		t.Errorf("the row's id line: %s", body)
	}
	if err := DshPutPreset(dir, "reviewer", "Reviews a diff.", "You review code.\n\nReport bugs only.\n"); err != nil {
		t.Fatal(err)
	}
	if user == readOf(t, f) {
		t.Error("the file didn't change")
	}
	cur, ours, err := DshPresetRow(dir, "reviewer")
	if err != nil || !ours {
		t.Fatalf("row: %q, ours %v, err %v", cur, ours, err)
	}
	if string(cur) != string(body) {
		t.Errorf("read back:\n%s\nwant:\n%s", cur, body)
	}
	if h := presetHash(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")); h == "" {
		t.Error("no hash")
	}
	// dsh's reading of the row: the registry's id and description, the
	// persona row's prefix
	n, err := dshNode(dshItem{id: "preset-magpie-reviewer", magpie: true, lines: strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n")})
	if err != nil {
		t.Fatalf("the row doesn't parse: %v\n%s", err, cur)
	}
	if v := yamlKey(yamlKey(n, "config"), "id"); v == nil || v.Value != "reviewer" {
		t.Errorf("config's id: %v", v)
	}
	persona := yamlKey(yamlKey(n, "config"), "plugins")
	if persona == nil || len(persona.Content) != 1 || yamlKey(persona.Content[0], "name").Value != dshPersonaPlugin {
		t.Fatalf("plugins: %v", persona)
	}
	if v := yamlKey(yamlKey(persona.Content[0], "config"), "prefix"); v == nil || v.Value != "You review code.\n\nReport bugs only." {
		t.Errorf("prefix: %v", v)
	}

	// written again, with the prompt changed: the persona row is replaced,
	// the user's registry row and their own preset stay as they are, and a
	// field of the user's in magpie's row (an order) stays too
	if err := os.WriteFile(f, []byte(strings.Replace(readOf(t, f), "  config:\n    id: reviewer", "  order: 9\n  config:\n    id: reviewer", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DshPutPreset(dir, "reviewer", "Reviews a diff.", "You review harder.\n"); err != nil {
		t.Fatal(err)
	}
	cur, _, _ = DshPresetRow(dir, "reviewer")
	if !strings.Contains(string(cur), "order: 9") {
		t.Errorf("the user's order went: %s", cur)
	}
	n, err = dshNode(dshItem{id: "preset-magpie-reviewer", magpie: true, lines: strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n")})
	if err != nil {
		t.Fatalf("the row doesn't parse after the rewrite: %v\n%s", err, cur)
	}
	if v := yamlKey(yamlKey(yamlKey(n, "config"), "plugins").Content[0], "config"); v == nil {
		t.Fatalf("the persona row's config went: %s", cur)
	} else if v := yamlKey(v, "prefix"); v == nil || v.Value != "You review harder." {
		t.Errorf("the persona row's prefix: %v", v)
	}
	if !strings.Contains(readOf(t, f), "id: preset-mine") || !strings.Contains(readOf(t, f), "default: standard") {
		t.Errorf("the user's rows went:\n%s", readOf(t, f))
	}

	// taken out while it is still magpie's: gone, the user's rows stay
	if err := DshDropPreset(dir, "reviewer", presetHash(strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n"))); err != nil {
		t.Fatal(err)
	}
	if cur, ours, _ := DshPresetRow(dir, "reviewer"); cur != nil || ours {
		t.Errorf("the row stayed: %q", cur)
	}
	if !strings.Contains(readOf(t, f), "id: preset-mine") {
		t.Errorf("the user's preset went with it:\n%s", readOf(t, f))
	}
}

func TestDshPresetRowNotOurs(t *testing.T) {
	home := dshPresetSandbox(t)
	dir := filepath.Join(home, ".dsh")
	f := dshPresetPatch(t, dir)
	row := strings.Join([]string{
		"- id: preset-magpie-reviewer",
		"  name: '@deepseek-ai/dsh-agent-preset'",
		"  config:",
		"    id: reviewer",
		"", // the file's last line
	}, "\n")
	dshPresetWrite(t, f, strings.TrimRight(readOf(t, f), "\n")+"\n"+row)

	if _, ours, err := DshPresetRow(dir, "reviewer"); err != nil || ours {
		t.Fatalf("ours: %v, err %v", ours, err)
	}
	if err := DshPutPreset(dir, "reviewer", "Reviews a diff.", "You review code.\n"); !errors.Is(err, ErrDshPresetNotOurs) {
		t.Fatalf("put: %v", err)
	}
	if err := DshDropPreset(dir, "reviewer", ""); !errors.Is(err, ErrDshPresetNotOurs) {
		t.Fatalf("drop: %v", err)
	}
	if !strings.Contains(readOf(t, f), "    id: reviewer") || strings.Contains(readOf(t, f), "# magpie") {
		t.Errorf("the user's row was touched:\n%s", readOf(t, f))
	}
}

func TestDshPresetDropKeepsTheUsersEdit(t *testing.T) {
	home := dshPresetSandbox(t)
	dir := filepath.Join(home, ".dsh")
	dshPresetPatch(t, dir)
	if err := DshPutPreset(dir, "reviewer", "Reviews a diff.", "You review code.\n"); err != nil {
		t.Fatal(err)
	}
	cur, _, err := DshPresetRow(dir, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DshPresetFile(dir), []byte(strings.Replace(readOf(t, DshPresetFile(dir)), "You review code.", "The user's own edit.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DshDropPreset(dir, "reviewer", presetHash(strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n"))); !errors.Is(err, ErrDshPresetNotOurs) {
		t.Fatalf("drop: %v", err)
	}
	if cur, _, _ := DshPresetRow(dir, "reviewer"); !strings.Contains(string(cur), "The user's own edit.") {
		t.Errorf("the user's edit went:\n%s", cur)
	}
}

func TestDshPresetNameOK(t *testing.T) {
	for name, want := range map[string]bool{
		"reviewer":    true,
		"a":           true,
		"code-review": true,
		"2fast":       false, // a digit may start the library's own, not dsh's
		"lead":        false, // the lead's own
		"-x":          false,
		"test-case-that-is-far-too-long-to-be-a-preset-id": false,
		strings.Repeat("a", 32):                            true,
		strings.Repeat("a", 33):                            false,
	} {
		if ok := DshPresetNameOK(name); ok != want {
			t.Errorf("DshPresetNameOK(%q) = %v", name, ok)
		}
	}
}

func TestDshPresetsOK(t *testing.T) {
	home := dshPresetSandbox(t)
	a := (&Agent{ID: "dsh", Dir: filepath.Join(home, ".dsh")})
	for v, want := range map[string]string{"0.2.1": "", "0.1.7": "", "0.1.7-rc.1": "", "0.1.6": "presets came"} {
		dshVersionIs(t, v)
		ok, why := DshPresetsOK(a)
		if ok != (want == "") {
			t.Errorf("dsh %s: ok %v, why %q", v, ok, why)
		}
		if want != "" && !strings.Contains(why, want) {
			t.Errorf("dsh %s: why %q", v, why)
		}
	}
	// no version on record: not written
	dshVersionIs(t, "")
	if ok, why := DshPresetsOK(a); ok || why == "" {
		t.Errorf("no version: ok %v, why %q", ok, why)
	}
	// no CLI, the desktop app's version instead
	dshVersionIs(t, "")
}

func readOf(t *testing.T, f string) string {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
