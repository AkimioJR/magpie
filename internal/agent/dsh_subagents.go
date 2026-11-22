package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yetone/magpie/internal/lazyre"
)

// dsh's subagents as magpie writes them: an @deepseek-ai/dsh-agent-preset
// row in the home patch list, $DSH_HOME/cordis.patch.yml, which dsh applies
// over every profile's own (#804). A preset is dsh's own shape of what the
// other agents keep as subagent files (library/subagents.go): an id the
// subagent tool asks for (SubagentStartRequest.agentPreset), a name and
// description its roster shows, and a child plugin list — magpie's is one
// @deepseek-ai/dsh-persona row whose prefix is the subagent's prompt.
//
// Presets came with packages/preset (dsh-v0.1.7-alpha.1, #4569): none of
// 0.1.6 has the package, so a dsh older than 0.1.7 is given none — its
// composition would refuse the row. The row is magpie's by its mark
// (dshMark), as its routes are: a row of another writer's is never
// touched. Within magpie's own row, the fields magpie owns are the
// preset's id, name, description and its persona row; what the user added
// beside them (an order, more plugin rows) stays. What magpie wrote is
// recorded by the hash of its lines (DshPresetRow), so a row the user
// changed is theirs from then on, as the subagent files are.

const (
	// dshPresetSince is the first dsh whose composition takes a
	// @deepseek-ai/dsh-agent-preset row: dsh-v0.1.7-alpha.1 is the first
	// tag with packages/preset, and none of 0.1.6 has it. 0.1.7's own
	// alphas and rcs say a version Newer puts below "0.1.7", so the gate
	// is the alpha's.
	dshPresetSince = "0.1.7-alpha.1"

	// dshPresetPlugin is the row a preset is declared on, and
	// dshPersonaPlugin the row the preset's prompt goes in (its prefix).
	dshPresetPlugin  = "@deepseek-ai/dsh-agent-preset"
	dshPersonaPlugin = "@deepseek-ai/dsh-persona"
)

// dshPresetRowID is the row id magpie writes its preset for subagent name
// under: preset- like dsh's own shipped ones (preset-standard), namespaced
// with magpie so a row of the user's is never the id magpie would write.
func dshPresetRowID(name string) string { return "preset-magpie-" + name }

// dshPresetNameRe is a preset id dsh's subagent tool takes: 1 to 32
// lowercase letters, digits and -, starting with a letter (its
// SubagentActivationSpec.name's rule), and not lead, the lead's own.
var dshPresetNameRe = lazyre.New(`^[a-z][a-z0-9-]{0,31}$`)

// DshPresetNameOK says whether the name is one dsh takes a preset id of.
// The library's own name rule is looser (a digit may start, 50 at most),
// so a subagent given dsh is checked again here.
func DshPresetNameOK(name string) bool {
	return name != "lead" && dshPresetNameRe.MatchString(name)
}

// DshPresetsOK says whether the dsh at a's home takes presets, and why not
// when it doesn't. The version is the one its CLI says, or its desktop
// app's when the CLI is not here: presets are the app's too, and both
// move together. A dsh with no version on record is not written: an older
// one's composition would refuse the row.
func DshPresetsOK(a *Agent) (bool, string) {
	v := dshVersion()
	if av := a.AppVersion(); av != "" && (v == "" || Newer(v, av)) {
		v = av
	}
	if v == "" {
		return false, "magpie can't tell how new this DeepSeek Harness is: its CLI said no version and it has no app here"
	}
	if Newer(dshPresetSince, v) {
		return false, "its presets came with dsh " + dshPresetSince + "; this one says " + v
	}
	return true, ""
}

// DshPresetFile is the patch list the presets go into: the home layer,
// which dsh applies over every profile's own, so one row there is every
// profile's.
func DshPresetFile(dir string) string { return dshHomePatch(dir) }

// DshPresetBody is the row magpie writes for a subagent called name with
// description and prompt: its lines, joined and ending in one newline, as
// DshPresetRow reads a written one back.
func DshPresetBody(name, description, prompt string) []byte {
	lines, err := dshNodeLines(dshPresetNode(name, description, prompt))
	if err != nil {
		return nil // magpie's own node, no user bytes in it
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// dshPresetNode is the row as YAML:
//
//   - id: preset-magpie-<name> # magpie
//     name: '@deepseek-ai/dsh-agent-preset'
//     config:
//     id: <name>
//     name: <name>
//     description: <description>
//     plugins:
//   - name: '@deepseek-ai/dsh-persona'
//     config:
//     prefix: <prompt>
func dshPresetNode(name, description, prompt string) *yaml.Node {
	str := func(s string, style yaml.Style) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: style}
	}
	persona := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("name", 0), str(dshPersonaPlugin, 0),
		str("config", 0),
		{Kind: yaml.MappingNode, Content: []*yaml.Node{
			str("prefix", 0),
			str(strings.TrimRight(prompt, "\n"), yaml.LiteralStyle),
		}},
	}}
	config := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("id", 0), str(name, 0),
		str("name", 0), str(name, 0),
		str("description", 0), str(description, 0),
		str("plugins", 0),
		{Kind: yaml.SequenceNode, Content: []*yaml.Node{persona}},
	}}
	value := str(dshPresetRowID(name), 0)
	value.LineComment = dshMark // on the value, as go-yaml reads a `- id: … # magpie` line back
	return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		str("id", 0), value,
		str("name", 0), str(dshPresetPlugin, 0),
		str("config", 0), config,
	}}
}

// DshPresetRow is what the home patch list at dir says about magpie's row
// for a subagent called name: its body as DshPresetBody would write it,
// nil when there is none, and ours whether the row there is magpie's by
// its mark.
func DshPresetRow(dir, name string) (body []byte, ours bool, err error) {
	_, items, err := dshRead(DshPresetFile(dir))
	if err != nil {
		return nil, false, err
	}
	i := dshFindLast(items, dshPresetRowID(name))
	if i < 0 {
		return nil, false, nil
	}
	return []byte(strings.Join(items[i].lines, "\n") + "\n"), items[i].magpie, nil
}

// ErrDshPresetNotOurs is what DshPutPreset and DshDropPreset say when the
// row they were given is one magpie didn't write, or one the user changed.
var ErrDshPresetNotOurs = errors.New("a preset row that isn't magpie's, or one the user changed, is left as it is")

// DshPutPreset writes the row for a subagent called name with description
// and prompt into the home patch list: over magpie's own row of the id
// (the fields magpie owns replaced, what the user added kept), or at its
// end. A row of the id that isn't magpie's is refused
// (ErrDshPresetNotOurs), and so is a list magpie can't edit.
func DshPutPreset(dir string, name, description, prompt string) error {
	dshWrites.Lock()
	defer dshWrites.Unlock()
	return dshPutPreset(dir, name, description, prompt)
}

func dshPutPreset(dir string, name, description, prompt string) error {
	f := DshPresetFile(dir)
	head, items, err := dshRead(f)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	row, err := dshNodeLines(dshPresetNode(name, description, prompt))
	if err != nil {
		return err
	}
	i := dshFindLast(items, dshPresetRowID(name))
	switch {
	case i < 0:
		items = append(items, dshItem{id: dshPresetRowID(name), magpie: true, lines: row})
	case !items[i].magpie:
		return ErrDshPresetNotOurs
	default:
		// magpie's own row: the fields magpie owns are replaced, what the
		// user added to it (an order, more plugin rows) stays
		if err := mergePresetRow(&items[i], dshItem{id: dshPresetRowID(name), lines: row}); err != nil {
			return err
		}
		items[i].magpie = true
	}
	return dshWrite(f, head, items)
}

// mergePresetRow takes fresh's id and config (the preset's name,
// description and persona row inside it) into the row have, leaving every
// other key, and every other row of the plugins list, as they are, and
// have's lines as the row reads then.
func mergePresetRow(have *dshItem, fresh dshItem) error {
	n, err := dshNode(*have)
	if err != nil {
		return err
	}
	f, err := dshNode(fresh)
	if err != nil {
		return err
	}
	for k := 0; k+1 < len(f.Content); k += 2 {
		switch f.Content[k].Value {
		case "id":
			yamlSetKey(n, "id", f.Content[k+1])
		case "config":
			config := yamlKey(n, "config")
			if config == nil || config.Kind != yaml.MappingNode {
				yamlSetKey(n, "config", f.Content[k+1])
				continue
			}
			mergePresetConfig(config, f.Content[k+1])
		}
	}
	have.lines, err = dshNodeLines(n)
	return err
}

func mergePresetConfig(config, fresh *yaml.Node) {
	for k := 0; k+1 < len(fresh.Content); k += 2 {
		switch fresh.Content[k].Value {
		case "id", "name", "description":
			yamlSetKey(config, fresh.Content[k].Value, fresh.Content[k+1])
		case "plugins":
			if v := yamlKey(config, "plugins"); v != nil && v.Kind == yaml.SequenceNode {
				mergePresetPlugins(v, fresh.Content[k+1])
				continue
			}
			yamlSetKey(config, "plugins", fresh.Content[k+1])
		}
	}
}

// mergePresetPlugins replaces the persona row in plugins, a sequence, with
// fresh's; one of the user's beside it stays where it is.
func mergePresetPlugins(plugins, fresh *yaml.Node) {
	for _, row := range fresh.Content {
		if n := yamlKey(row, "name"); n == nil || n.Value != dshPersonaPlugin {
			continue
		}
		for i, have := range plugins.Content {
			if n := yamlKey(have, "name"); n != nil && n.Value == dshPersonaPlugin {
				plugins.Content[i] = row
				return
			}
		}
		plugins.Content = append(plugins.Content, row)
		return
	}
}

// DshDropPreset takes magpie's row for a subagent called name out of the
// home patch list, and only while it is still as hash says it was when
// magpie last wrote it: a row the user changed, or one that isn't
// magpie's, is left as it is (ErrDshPresetNotOurs). No row, nothing done.
func DshDropPreset(dir, name string, hash string) error {
	dshWrites.Lock()
	defer dshWrites.Unlock()
	f := DshPresetFile(dir)
	head, items, err := dshRead(f)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	i := dshFindLast(items, dshPresetRowID(name))
	if i < 0 {
		return nil
	}
	if !items[i].magpie || presetHash(items[i].lines) != hash {
		return ErrDshPresetNotOurs
	}
	items = append(items[:i], items[i+1:]...)
	return dshWrite(f, head, items)
}

// presetHash is what the library records of a row: the hash of its body as
// DshPresetBody and DshPresetRow give it.
func presetHash(lines []string) string {
	b := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return hex.EncodeToString(b[:])
}
