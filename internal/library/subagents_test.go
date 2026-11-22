package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/yetone/magpie/internal/testenv"
)

// subagentAgents puts every agent that takes subagents in the sandbox.
func subagentAgents(t *testing.T, h string) map[string]string {
	t.Helper()
	write(t, filepath.Join(h, ".qwen/settings.json"), "{}\n")
	write(t, filepath.Join(h, ".factory/settings.json"), "")
	write(t, filepath.Join(h, ".grok/config.toml"), "")
	// ZCode is a desktop app found by its config: no CLI beside the folder
	write(t, filepath.Join(h, ".zcode/v2/config.json"), "{}\n")
	// dsh takes a preset row in its home patch list, not a file of its own;
	// its CLI beside the folder says the version the presets came with
	testenv.Program(t, filepath.Join(h, "bin", "dsh"), "#!/bin/sh\necho 0.2.1\n")
	write(t, filepath.Join(h, "bin", "dsh.exe"), "")
	return map[string]string{
		"claude":   filepath.Join(h, ".claude/agents/reviewer.md"),
		"codex":    filepath.Join(h, ".codex/agents/reviewer.toml"),
		"opencode": filepath.Join(h, ".config/opencode/agents/reviewer.md"),
		"gemini":   filepath.Join(h, ".gemini/agents/reviewer.md"),
		"qwen":     filepath.Join(h, ".qwen/agents/reviewer.md"),
		"droid":    filepath.Join(h, ".factory/droids/reviewer.md"),
		"grok":     filepath.Join(h, ".grok/agents/reviewer.md"),
		"cursor":   filepath.Join(h, ".cursor/agents/reviewer.md"),
		"copilot":  filepath.Join(h, ".copilot/agents/reviewer.agent.md"),
		"zcode":    filepath.Join(h, ".zcode/agents/reviewer.md"),
		"dsh":      filepath.Join(h, ".dsh/cordis.patch.yml"),
	}
}

var reviewer = Subagent{
	Name:        "reviewer",
	Description: "Reviews a diff: \"bugs\" only.\n  Use after edits.",
	Prompt:      "You review code.\r\n\r\nReport bugs only.\n\n",
	MCP:         []string{"github", "gone"},
	Skills:      []string{"tdd", "gone"},
}

// Each agent gets the subagent as a file of its own, in its own folder
// and shape: the skills and servers only where the agent's file can name
// them, and of them only those the library has.
func TestSubagentsEachAgentsFile(t *testing.T) {
	h := sandbox(t)
	files := subagentAgents(t, h)
	ok(t)(SaveServer("", Server{Name: "github", Transport: "stdio", Command: "npx", Agents: []string{"claude", "droid"}}))
	ok(t)(change(func(l *Library) error {
		l.Skills = append(l.Skills, &Skill{Name: "tdd", Agents: []string{}})
		return nil
	}))
	s := reviewer
	for id := range files {
		s.Agents = append(s.Agents, id)
	}
	ok(t)(SaveSubagent("", s))

	desc := `"Reviews a diff: \"bugs\" only. Use after edits."`
	body := "---\n\nYou review code.\n\nReport bugs only.\n"
	want := map[string]string{
		"claude":   "---\nname: \"reviewer\"\ndescription: " + desc + "\nskills: [\"tdd\"]\nmcpServers: [\"github\"]\n" + body,
		"opencode": "---\ndescription: " + desc + "\nmode: subagent\n" + body,
		"droid":    "---\nname: \"reviewer\"\ndescription: " + desc + "\nmcpServers: [\"github\"]\n" + body,
	}
	for _, id := range []string{"gemini", "qwen", "grok", "cursor", "copilot", "zcode"} {
		want[id] = "---\nname: \"reviewer\"\ndescription: " + desc + "\n" + body
	}
	for id, f := range files {
		got := read(t, f)
		if id == "codex" {
			var role map[string]any
			if err := toml.Unmarshal([]byte(got), &role); err != nil {
				t.Fatalf("codex role: %v\n%s", err, got)
			}
			if role["name"] != "reviewer" || role["description"] != `Reviews a diff: "bugs" only. Use after edits.` ||
				role["developer_instructions"] != "You review code.\n\nReport bugs only.\n" || len(role) != 3 {
				t.Errorf("codex role: %#v", role)
			}
			continue
		}
		if id == "dsh" {
			// its preset row in the home patch list, magpie's by its mark
			for _, piece := range []string{
				"- id: preset-magpie-reviewer # magpie",
				"name: '@deepseek-ai/dsh-agent-preset'",
				"    id: reviewer",
				"      - name: '@deepseek-ai/dsh-persona'",
				"You review code.",
			} {
				if !strings.Contains(got, piece) {
					t.Errorf("dsh's patch list misses %q:\n%s", piece, got)
				}
			}
			continue
		}
		if got != want[id] {
			t.Errorf("%s's %s:\n%s\nwant:\n%s", id, f, got, want[id])
		}
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Subagents) != 1 || len(v.Subagents[0].Files) != len(files) || len(v.Subagents[0].Problems) != 0 {
		t.Errorf("view: %+v", v.Subagents)
	}
	for _, av := range v.SubagentAgents {
		if (av.Skills != (av.ID == "claude")) || (av.MCP != (av.ID == "claude" || av.ID == "droid")) || av.No != "" {
			t.Errorf("agent view %+v", av)
		}
	}

	// taken from one agent: its file goes, the others stay
	ok(t)(SubagentAgentsSet("reviewer", []string{"claude", "codex"}))
	for id, f := range files {
		if id == "dsh" {
			// the patch list stays (dsh wants a list, even an empty one):
			// magpie's row goes
			if got := read(t, f); strings.Contains(got, "preset-magpie-reviewer") {
				t.Errorf("dsh's row after untick:\n%s", got)
			}
			continue
		}
		_, err := os.Stat(f)
		if gone := os.IsNotExist(err); gone != (id != "claude" && id != "codex") {
			t.Errorf("%s's file after untick: %v", id, err)
		}
	}
	// renamed: the old name's files go, the new one's come
	s.Name, s.Agents = "critic", []string{"claude", "codex"}
	ok(t)(SaveSubagent("reviewer", s))
	if read(t, files["claude"]) != "" || read(t, files["codex"]) != "" {
		t.Error("the old name's files stayed")
	}
	if !strings.Contains(read(t, filepath.Join(h, ".claude/agents/critic.md")), "name: \"critic\"") {
		t.Error("no critic.md")
	}
	// a file the user deleted comes back on the next sync
	os.Remove(filepath.Join(h, ".claude/agents/critic.md"))
	ok(t)(Sync())
	if read(t, filepath.Join(h, ".claude/agents/critic.md")) == "" {
		t.Error("the deleted file wasn't written again")
	}
	ok(t)(RemoveSubagents([]string{"critic"}))
	for _, f := range []string{".claude/agents/critic.md", ".codex/agents/critic.toml"} {
		if _, err := os.Stat(filepath.Join(h, f)); !os.IsNotExist(err) {
			t.Errorf("%s after remove: %v", f, err)
		}
	}
}

// A subagent file of the user's own by the same name is never written
// over, nor taken away; one magpie wrote that the user edited is theirs
// from then on: a change in the library leaves it as the user has it.
func TestSubagentsLeaveTheUsersFiles(t *testing.T) {
	h := sandbox(t)
	own := filepath.Join(h, ".claude/agents/reviewer.md")
	write(t, own, "---\nname: reviewer\ndescription: mine\n---\nMy own reviewer.\n")
	s := reviewer
	s.Agents = []string{"claude", "codex", "opencode"}
	res, err := SaveSubagent("", s)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || res.Problems[0].Agent != "claude" || res.Problems[0].What != "subagent:reviewer" {
		t.Fatalf("problems: %+v", res.Problems)
	}
	if read(t, own) != "---\nname: reviewer\ndescription: mine\n---\nMy own reviewer.\n" {
		t.Fatalf("the user's own file was written over:\n%s", read(t, own))
	}

	// the user edits magpie's Codex file, then the library's subagent
	// changes: the edit stays, and the page is told why
	codex := filepath.Join(h, ".codex/agents/reviewer.toml")
	oc := filepath.Join(h, ".config/opencode/agents/reviewer.md")
	edited := read(t, codex) + "model = \"gpt-5.5\"\n"
	write(t, codex, edited)
	s.Prompt = "You review code, twice."
	res, err = SaveSubagent("reviewer", s)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, codex) != edited {
		t.Errorf("the user's edit was written over:\n%s", read(t, codex))
	}
	if !strings.Contains(read(t, oc), "You review code, twice.") {
		t.Errorf("magpie's untouched OpenCode file wasn't updated:\n%s", read(t, oc))
	}
	var codexSaid bool
	for _, p := range res.Problems {
		codexSaid = codexSaid || p.Agent == "codex" && strings.Contains(p.Error, "of its own")
	}
	if !codexSaid {
		t.Errorf("problems: %+v", res.Problems)
	}

	// removed from the library: magpie's own file goes, the user's stay
	if _, err := RemoveSubagents([]string{"reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oc); !os.IsNotExist(err) {
		t.Errorf("magpie's OpenCode file stayed: %v", err)
	}
	if read(t, codex) != edited || !strings.Contains(read(t, own), "My own reviewer.") {
		t.Error("removing took the user's files")
	}
	l, _ := load()
	for id, a := range l.Applied {
		if len(a.Subagents) != 0 {
			t.Errorf("%s still records %+v", id, a.Subagents)
		}
	}
}

func TestSubagentCheck(t *testing.T) {
	sandbox(t)
	for _, s := range []Subagent{
		{Name: "Reviewer", Description: "d", Prompt: "p"},
		{Name: "r", Description: "d", Prompt: "p"},
		{Name: "-r-", Description: "d", Prompt: "p"},
		{Name: "default", Description: "d", Prompt: "p"},
		{Name: "rev", Description: " ", Prompt: "p"},
		{Name: "rev", Description: "d", Prompt: " \n"},
		{Name: "rev", Description: "d", Prompt: "p", Agents: []string{"crush"}},
	} {
		if _, err := SaveSubagent("", s); err == nil {
			t.Errorf("%+v saved", s)
		}
	}
}

// A subagent's model (luckymanjc on Discord: a routing group set for a
// subagent, which Codex ran on its lead's model) goes into Codex's role
// file, whose model Codex lays over the lead's pick; no other agent's file
// is written one, and the page is told only Codex takes it. Changed in the
// library, the role file magpie wrote changes with it; taken away, the
// role is as one written without a model.
func TestSubagentModelGoesToCodex(t *testing.T) {
	h := sandbox(t)
	files := subagentAgents(t, h)
	s := reviewer
	for id := range files {
		s.Agents = append(s.Agents, id)
	}
	ok(t)(SaveSubagent("", s))
	plain := map[string]string{}
	for id, f := range files {
		plain[id] = read(t, f)
	}
	role := func() map[string]any {
		t.Helper()
		var r map[string]any
		if err := toml.Unmarshal([]byte(read(t, files["codex"])), &r); err != nil {
			t.Fatalf("codex role: %v\n%s", err, read(t, files["codex"]))
		}
		return r
	}

	s.Model = " group/review "
	ok(t)(SaveSubagent("reviewer", s))
	if r := role(); r["model"] != "group/review" || r["developer_instructions"] != "You review code.\n\nReport bugs only.\n" || len(r) != 4 {
		t.Errorf("codex role with a model: %#v", r)
	}
	for id, f := range files {
		if id != "codex" && read(t, f) != plain[id] {
			t.Errorf("%s's file changed with the model:\n%s", id, read(t, f))
		}
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Subagents[0].Model != "group/review" {
		t.Errorf("view's model: %q", v.Subagents[0].Model)
	}
	for _, av := range v.SubagentAgents {
		if av.Model != (av.ID == "codex") {
			t.Errorf("agent view %+v", av)
		}
	}

	s.Model = "gpt-6-sol"
	ok(t)(SaveSubagent("reviewer", s))
	if r := role(); r["model"] != "gpt-6-sol" {
		t.Errorf("codex role after the model changed: %#v", r)
	}
	s.Model = ""
	ok(t)(SaveSubagent("reviewer", s))
	if got := read(t, files["codex"]); got != plain["codex"] {
		t.Errorf("codex role with the model taken away:\n%s\nwant:\n%s", got, plain["codex"])
	}

	for _, m := range []string{"group/a b", "x\ny", strings.Repeat("m", 201)} {
		s.Model = m
		if _, err := SaveSubagent("reviewer", s); err == nil {
			t.Errorf("model %q saved", m)
		}
	}
}

// dsh's row goes by the rows' own discipline: a row of magpie's row id
// that the user wrote is never touched, a problem; one magpie wrote that
// the user changed is theirs from then on; unticking takes magpie's own
// out. A name dsh's preset ids don't take is refused for dsh alone.
func TestSubagentsDshRows(t *testing.T) {
	h := sandbox(t)
	files := subagentAgents(t, h)
	patch := files["dsh"]

	// a row of the id magpie would write, the user's own: left, a problem
	write(t, patch, strings.Join([]string{
		"- id: preset-magpie-reviewer",
		"  name: '@deepseek-ai/dsh-agent-preset'",
		"  config:",
		"    id: reviewer",
		"",
	}, "\n"))
	s := reviewer
	s.Agents = []string{"dsh"}
	res, err := SaveSubagent("", s)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || res.Problems[0].Agent != "dsh" {
		t.Fatalf("problems: %+v", res.Problems)
	}
	if got := read(t, patch); !strings.Contains(got, "    id: reviewer") || strings.Contains(got, "# magpie") {
		t.Errorf("the user's row was touched:\n%s", got)
	}

	// their row under its own id, and magpie's beside it
	write(t, patch, strings.Join([]string{
		"- id: preset-theirs",
		"  name: '@deepseek-ai/dsh-agent-preset'",
		"  config:",
		"    id: theirs",
		"",
	}, "\n"))
	res, err = SaveSubagent("reviewer", s)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %+v", res.Problems)
	}
	got := read(t, patch)
	if !strings.Contains(got, "- id: preset-magpie-reviewer # magpie") || !strings.Contains(got, "id: preset-theirs") {
		t.Errorf("the rows:\n%s", got)
	}

	// the user edits magpie's row; a change in the library leaves it as
	// the user has it
	write(t, patch, strings.Replace(got, "You review code.", "The user's own edit.", 1))
	s.Prompt = "You review code, twice."
	res, err = SaveSubagent("reviewer", s)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || res.Problems[0].Agent != "dsh" {
		t.Fatalf("problems: %+v", res.Problems)
	}
	if got = read(t, patch); !strings.Contains(got, "The user's own edit.") {
		t.Errorf("the user's edit went:\n%s", got)
	}

	// unticked: magpie's row is the user's now, and stays
	ok(t)(SubagentAgentsSet("reviewer", nil))
	if got = read(t, patch); !strings.Contains(got, "preset-magpie-reviewer") {
		t.Errorf("the user's row was taken:\n%s", got)
	}

	// a name dsh's preset ids don't take is refused for dsh alone
	s.Name = "2fast"
	s.Prompt = "You review code."
	s.Agents = []string{"dsh"}
	if _, err := SaveSubagent("", s); err == nil {
		t.Error("2fast saved for dsh")
	}
	s.Agents = []string{"claude"}
	if _, err := SaveSubagent("", s); err != nil {
		t.Errorf("2fast for claude: %v", err)
	}
}
