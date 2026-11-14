package agent

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// vscodeProtocolHome wires a fake home with providers spanning all three
// protocols (Responses, Messages adaptive/budget/plain, Chat Completions).
func vscodeProtocolHome(t *testing.T) (home, dir, lm string) {
	t.Helper()
	home = syncHome(t)
	for _, p := range []provider.Provider{
		{ID: "resp", Name: "Resp", Key: "k", Responses: "http://127.0.0.1:1/v1", Models: []string{"gpt-5.5"}},
		{ID: "anth", Name: "Anth", Key: "k", Anthropic: "http://127.0.0.1:1", Models: []string{"claude-sonnet-4-6", "claude-3-7-sonnet", "claude-haiku-4-5"}},
		{ID: "chat", Name: "Chat", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"glm-4.6"}},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.SaveLive("resp", "http://127.0.0.1:1/v1", []catalog.Model{
		{ID: "gpt-5.5", Reasoning: true, Efforts: []string{"low", "medium", "high"}, Context: 400000, Output: 128000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("anth", "http://127.0.0.1:1", []catalog.Model{
		{ID: "claude-sonnet-4-6", Reasoning: true, Efforts: []string{"low", "high", "max"}, Context: 200000, Output: 64000},
		{ID: "claude-3-7-sonnet", Reasoning: true, Context: 200000, Output: 16384},
		{ID: "claude-haiku-4-5", Reasoning: false, Context: 200000, Output: 8192},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("chat", "http://127.0.0.1:1/v1", []catalog.Model{
		{ID: "glm-4.6", Reasoning: true, Efforts: []string{"low", "high"}, Context: 204800, Output: 8192},
	}); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(home, "vscode", "User")
	lm = filepath.Join(dir, "chatLanguageModels.json")
	writeFile(t, filepath.Join(dir, "settings.json"), `{"editor.fontSize":14}`)
	writeFile(t, lm, `[]`)
	return home, dir, lm
}

func vscodeModelByID(g, id string) gjson.Result {
	for _, m := range gjson.Get(g, "models").Array() {
		if m.Get("id").String() == id {
			return m
		}
	}
	return gjson.Result{}
}

// Auto protocol selection gives each model its own apiType, URL,
// thinking flags/budgets, and reasoningEffortFormat based on its catalog APIs.
func TestVSCodeProtocolAuto(t *testing.T) {
	_, dir, lm := vscodeProtocolHome(t)
	a := vscodeAt(dir)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if got := a.Check(); got != "" {
		t.Fatalf("connected check: %q", got)
	}

	g, ok := edit.GetJSONItem(lm, vscodeGroup)
	if !ok {
		t.Fatalf("magpie group missing:\n%s", readFile(lm))
	}
	// Group fallback remains chat-completions unless overridden, while each model
	// declares its own apiType when automatic.
	if got := gjson.Get(g, "apiType").String(); got != "chat-completions" {
		t.Errorf("group apiType = %q, want chat-completions", got)
	}

	// 1. Responses model with reasoning + efforts
	mResp := vscodeModelByID(g, "resp/gpt-5.5")
	if !mResp.Exists() {
		t.Fatalf("missing resp/gpt-5.5 in %s", g)
	}
	if mResp.Get("apiType").String() != "responses" || mResp.Get("url").String() != gatewayV1()+"/responses" {
		t.Errorf("resp model apiType/url = %q / %q", mResp.Get("apiType").String(), mResp.Get("url").String())
	}
	if !mResp.Get("thinking").Bool() {
		t.Errorf("resp model thinking should be true: %s", mResp.Raw)
	}
	if mResp.Get("reasoningEffortFormat").String() != "responses" {
		t.Errorf("resp model reasoningEffortFormat = %q, want responses", mResp.Get("reasoningEffortFormat").String())
	}
	if got := mResp.Get("supportsReasoningEffort.#").Int(); got != 3 {
		t.Errorf("resp model supportsReasoningEffort count = %d, want 3", got)
	}

	// 2. Adaptive-thinking Messages model (claude-sonnet-4-6)
	mAdap := vscodeModelByID(g, "anth/claude-sonnet-4-6")
	if !mAdap.Exists() {
		t.Fatalf("missing anth/claude-sonnet-4-6 in %s", g)
	}
	if mAdap.Get("apiType").String() != "messages" || mAdap.Get("url").String() != gatewayV1()+"/messages" {
		t.Errorf("adaptive messages apiType/url = %q / %q", mAdap.Get("apiType").String(), mAdap.Get("url").String())
	}
	if !mAdap.Get("thinking").Bool() || !mAdap.Get("adaptiveThinking").Bool() {
		t.Errorf("adaptive messages thinking/adaptiveThinking = %v / %v", mAdap.Get("thinking").Bool(), mAdap.Get("adaptiveThinking").Bool())
	}
	if mAdap.Get("reasoningEffortFormat").String() != "messages" {
		t.Errorf("adaptive messages reasoningEffortFormat = %q, want messages", mAdap.Get("reasoningEffortFormat").String())
	}
	if mAdap.Get("minThinkingBudget").Exists() || mAdap.Get("maxThinkingBudget").Exists() {
		t.Errorf("adaptive messages should not set thinking budgets: %s", mAdap.Raw)
	}

	// 3. Budget-based thinking Messages model (claude-3-7-sonnet)
	mBudget := vscodeModelByID(g, "anth/claude-3-7-sonnet")
	if !mBudget.Exists() {
		t.Fatalf("missing anth/claude-3-7-sonnet in %s", g)
	}
	if mBudget.Get("apiType").String() != "messages" || mBudget.Get("url").String() != gatewayV1()+"/messages" {
		t.Errorf("budget messages apiType/url = %q / %q", mBudget.Get("apiType").String(), mBudget.Get("url").String())
	}
	if !mBudget.Get("thinking").Bool() || mBudget.Get("adaptiveThinking").Exists() {
		t.Errorf("budget messages thinking/adaptive = %v / %v", mBudget.Get("thinking").Bool(), mBudget.Get("adaptiveThinking").Exists())
	}
	if minB, maxB := mBudget.Get("minThinkingBudget").Int(), mBudget.Get("maxThinkingBudget").Int(); minB != 1024 || maxB <= minB || maxB > 32000 {
		t.Errorf("budget messages min/max = %d / %d", minB, maxB)
	}

	// 4. Non-reasoning Messages model (claude-haiku-4-5)
	mPlain := vscodeModelByID(g, "anth/claude-haiku-4-5")
	if !mPlain.Exists() {
		t.Fatalf("missing anth/claude-haiku-4-5 in %s", g)
	}
	if mPlain.Get("apiType").String() != "messages" || mPlain.Get("thinking").Exists() || mPlain.Get("adaptiveThinking").Exists() || mPlain.Get("minThinkingBudget").Exists() {
		t.Errorf("non-reasoning messages model unexpected thinking fields: %s", mPlain.Raw)
	}

	// 5. Chat Completions model with reasoning + efforts
	mChat := vscodeModelByID(g, "chat/glm-4.6")
	if !mChat.Exists() {
		t.Fatalf("missing chat/glm-4.6 in %s", g)
	}
	if mChat.Get("apiType").String() != "chat-completions" || mChat.Get("url").String() != gatewayV1()+"/chat/completions" {
		t.Errorf("chat model apiType/url = %q / %q", mChat.Get("apiType").String(), mChat.Get("url").String())
	}
	if !mChat.Get("thinking").Bool() || mChat.Get("reasoningEffortFormat").String() != "chat-completions" {
		t.Errorf("chat model thinking/format = %v / %q", mChat.Get("thinking").Bool(), mChat.Get("reasoningEffortFormat").String())
	}

	// Check detects stale URL even when the first model uses Responses/Messages.
	first := gjson.Get(g, "models.0")
	firstAPI := first.Get("apiType").String()
	staleSuffix := "/v1/chat/completions"
	switch firstAPI {
	case "responses":
		staleSuffix = "/v1/responses"
	case "messages":
		staleSuffix = "/v1/messages"
	}
	if err := edit.SetJSONItem(lm, vscodeGroup, map[string]any{
		"name":    magpieID,
		"vendor":  vscodeEndpoint,
		"apiType": "chat-completions",
		"models": []any{map[string]any{
			"id":             first.Get("id").String(),
			"apiType":        firstAPI,
			"url":            "http://127.0.0.1:9999" + staleSuffix,
			"requestHeaders": map[string]string{"Authorization": "Bearer " + vscodeStable.token()},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if ch := a.Check(); ch == "" {
		t.Fatalf("Check missed stale URL when first model uses %q", firstAPI)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if ch := a.Check(); ch != "" {
		t.Fatalf("after Sync Check() = %q", ch)
	}
}

// Manual protocol selection works via the protocol Field across profiles,
// clears back to auto on empty string, survives Disconnect/Reconnect (or resets
// cleanly on Disconnect), and rejects invalid protocol names.
func TestVSCodeProtocolManualAndProfiles(t *testing.T) {
	_, dir, lm := vscodeProtocolHome(t)
	profileLM := filepath.Join(dir, "profiles", "p1", "chatLanguageModels.json")
	writeFile(t, filepath.Join(dir, "globalStorage", "storage.json"), `{"userDataProfiles":[{"location":"p1","name":"Work"}]}`)
	writeFile(t, filepath.Join(dir, "profiles", "p1", "settings.json"), `{}`)
	writeFile(t, profileLM, `[]`)

	a := vscodeAt(dir)
	f := a.Field("protocol")
	if f == nil || !f.Quiet {
		t.Fatalf("protocol field missing or not quiet: %+v", f)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if got := f.Get(); got != "" {
		t.Fatalf("initial protocol = %q, want empty (auto)", got)
	}
	opts := f.Options(map[string]string{"language": "en"})
	var values []string
	for _, o := range opts {
		values = append(values, o.Value)
	}
	if !slices.Equal(values, []string{"", "chat-completions", "responses", "messages"}) {
		t.Fatalf("options = %v", values)
	}

	if err := f.Set("bogus"); err == nil {
		t.Fatal("accepted invalid protocol")
	}

	// Pin all models to responses
	if err := f.Set("responses"); err != nil {
		t.Fatal(err)
	}
	if got := f.Get(); got != "responses" {
		t.Fatalf("protocol after set = %q, want responses", got)
	}
	for _, target := range []string{lm, profileLM} {
		g, ok := edit.GetJSONItem(target, vscodeGroup)
		if !ok {
			t.Fatalf("%s missing magpie group", target)
		}
		if gjson.Get(g, "apiType").String() != "responses" {
			t.Errorf("%s group apiType = %q, want responses", target, gjson.Get(g, "apiType").String())
		}
		for _, m := range gjson.Get(g, "models").Array() {
			if m.Get("apiType").String() != "responses" || m.Get("url").String() != gatewayV1()+"/responses" {
				t.Errorf("%s model %s apiType/url = %q / %q", target, m.Get("id").String(), m.Get("apiType").String(), m.Get("url").String())
			}
			if m.Get("supportsReasoningEffort").Exists() && m.Get("reasoningEffortFormat").String() != "responses" {
				t.Errorf("%s model %s reasoningEffortFormat = %q", target, m.Get("id").String(), m.Get("reasoningEffortFormat").String())
			}
		}
	}
	if ch := a.Check(); ch != "" {
		t.Fatalf("Check after manual responses = %q", ch)
	}

	// Pin all models to messages
	if err := f.Set("messages"); err != nil {
		t.Fatal(err)
	}
	g, _ := edit.GetJSONItem(lm, vscodeGroup)
	if gjson.Get(g, "apiType").String() != "messages" {
		t.Fatalf("group apiType = %q, want messages", gjson.Get(g, "apiType").String())
	}
	if mChat := vscodeModelByID(g, "chat/glm-4.6"); mChat.Get("apiType").String() != "messages" || mChat.Get("url").String() != gatewayV1()+"/messages" || mChat.Get("reasoningEffortFormat").String() != "messages" {
		t.Errorf("glm-4.6 pinned to messages: %s", mChat.Raw)
	}

	// Reset to auto ("") restores per-model protocols
	if err := f.Set(""); err != nil {
		t.Fatal(err)
	}
	if got := f.Get(); got != "" {
		t.Fatalf("protocol after reset = %q, want empty", got)
	}
	g, _ = edit.GetJSONItem(lm, vscodeGroup)
	if vscodeModelByID(g, "resp/gpt-5.5").Get("apiType").String() != "responses" ||
		vscodeModelByID(g, "anth/claude-sonnet-4-6").Get("apiType").String() != "messages" ||
		vscodeModelByID(g, "chat/glm-4.6").Get("apiType").String() != "chat-completions" {
		t.Fatalf("reset to auto did not restore per-model protocols: %s", g)
	}

	// Pick via Applied resets on Disconnect cleanly
	if err := a.Apply("protocol", "responses"); err != nil {
		t.Fatal(err)
	}
	if f.Get() != "responses" {
		t.Fatalf("after Apply: %q", f.Get())
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if f.Get() != "" {
		t.Fatalf("after Disconnect protocol = %q, want empty", f.Get())
	}
	if _, ok := edit.GetJSONItem(lm, vscodeGroup); ok {
		t.Fatalf("magpie group left after Disconnect")
	}
}

// Hand-edited apiType on individual models or on the group in chatLanguageModels.json
// survives Sync() with matching URL/effort format/budgets, and an explicit
// Protocol.Set overrides the hand edit cleanly.
func TestVSCodeProtocolHandEditPreservedAndOverridden(t *testing.T) {
	_, dir, lm := vscodeProtocolHome(t)
	a := vscodeAt(dir)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}

	// Simulate user hand-editing chat/glm-4.6 to "messages" and resp/gpt-5.5 to "chat-completions"
	g, ok := edit.GetJSONItem(lm, vscodeGroup)
	if !ok {
		t.Fatal("missing group")
	}
	var rawModels []map[string]any
	for _, m := range gjson.Get(g, "models").Array() {
		item := m.Value().(map[string]any)
		switch m.Get("id").String() {
		case "chat/glm-4.6":
			item["apiType"] = "messages"
		case "resp/gpt-5.5":
			item["apiType"] = "chat-completions"
		}
		rawModels = append(rawModels, item)
	}
	if err := edit.SetJSONItem(lm, vscodeGroup, map[string]any{
		"name": magpieID, "vendor": vscodeEndpoint, "apiType": "chat-completions", "models": rawModels,
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	g, _ = edit.GetJSONItem(lm, vscodeGroup)
	mGlm := vscodeModelByID(g, "chat/glm-4.6")
	mResp := vscodeModelByID(g, "resp/gpt-5.5")
	if mGlm.Get("apiType").String() != "messages" || mGlm.Get("url").String() != gatewayV1()+"/messages" || mGlm.Get("reasoningEffortFormat").String() != "messages" {
		t.Errorf("hand-edited glm-4.6 lost across Sync: %s", mGlm.Raw)
	}
	if mResp.Get("apiType").String() != "chat-completions" || mResp.Get("url").String() != gatewayV1()+"/chat/completions" || mResp.Get("reasoningEffortFormat").String() != "chat-completions" {
		t.Errorf("hand-edited resp/gpt-5.5 lost across Sync: %s", mResp.Raw)
	}

	// Group-level hand edit: change group apiType to "responses" and remove per-model
	// apiType on anth/claude-haiku-4-5; Sync preserves the user's group edit on that model
	// rather than overwriting group apiType or forgetting the group edit.
	rawModels = nil
	for _, m := range gjson.Get(g, "models").Array() {
		item := m.Value().(map[string]any)
		if m.Get("id").String() == "anth/claude-haiku-4-5" {
			delete(item, "apiType")
		}
		rawModels = append(rawModels, item)
	}
	if err := edit.SetJSONItem(lm, vscodeGroup, map[string]any{
		"name": magpieID, "vendor": vscodeEndpoint, "apiType": "responses", "models": rawModels,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	g, _ = edit.GetJSONItem(lm, vscodeGroup)
	if gjson.Get(g, "apiType").String() != "responses" {
		t.Errorf("hand-edited group apiType lost on Sync: %s", g)
	}
	if got := vscodeModelByID(g, "anth/claude-haiku-4-5").Get("url").String(); got != gatewayV1()+"/responses" {
		t.Errorf("model inheriting hand-edited group apiType got url = %q, want %s/responses", got, gatewayV1())
	}

	// Explicit Protocol.Set overwrites hand edits back to explicit choice or auto.
	f := a.Field("protocol")
	if f == nil {
		t.Fatal("missing protocol field")
	}
	if err := f.Set("chat-completions"); err != nil {
		t.Fatal(err)
	}
	g, _ = edit.GetJSONItem(lm, vscodeGroup)
	if vscodeModelByID(g, "resp/gpt-5.5").Get("apiType").String() != "chat-completions" ||
		vscodeModelByID(g, "anth/claude-sonnet-4-6").Get("apiType").String() != "chat-completions" {
		t.Errorf("Protocol.Set did not overwrite hand edits: %s", g)
	}

	// And switching to auto ("") resets hand edits to catalog defaults cleanly.
	if err := f.Set(""); err != nil {
		t.Fatal(err)
	}
	g, _ = edit.GetJSONItem(lm, vscodeGroup)
	if vscodeModelByID(g, "resp/gpt-5.5").Get("apiType").String() != "responses" ||
		vscodeModelByID(g, "chat/glm-4.6").Get("apiType").String() != "chat-completions" {
		t.Errorf("Protocol.Set(\"\") did not restore auto defaults: %s", g)
	}
}

// VSCodium on older Copilot Chat (0.48.1, customoai) hides protocol options
// and stays on chat/completions, while customendpoint (0.50.0) enables it.
func TestVSCodiumProtocolCustomOAIFallback(t *testing.T) {
	home, _, _ := vscodeProtocolHome(t)
	installCopilotChat(t, home, "0.48.1", copilotChat0481Providers)
	c, err := Find("vscodium")
	if err != nil {
		t.Fatal(err)
	}
	cDir := c.Dir
	lm := filepath.Join(cDir, "chatLanguageModels.json")
	writeFile(t, c.Path, `{}`)
	writeFile(t, lm, `[]`)

	f := c.Field("protocol")
	if f == nil {
		t.Fatal("vscodium missing protocol field")
	}
	if opts := f.Options(map[string]string{"language": "en"}); opts != nil {
		t.Fatalf("0.48.1 customoai should expose no protocol options, got %+v", opts)
	}
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	g, ok := edit.GetJSONItem(lm, vscodeGroupOf(vscodeOAI))
	if !ok {
		t.Fatalf("missing customoai group:\n%s", readFile(lm))
	}
	if gjson.Get(g, "apiType").Exists() || gjson.Get(g, "models.0.apiType").Exists() {
		t.Errorf("customoai must not set apiType: %s", g)
	}
	if gjson.Get(g, "models.0.url").String() != gatewayV1()+"/chat/completions" {
		t.Errorf("customoai url = %q", gjson.Get(g, "models.0.url").String())
	}

	// Upgrading Copilot Chat to customendpoint exposes protocol options and
	// applies per-model auto protocols on Sync.
	installCopilotChat(t, home, "0.50.0", `[{"vendor":"copilot"},{"vendor":"customendpoint"},{"vendor":"customoai"}]`)
	if opts := f.Options(map[string]string{"language": "en"}); len(opts) == 0 {
		t.Fatal("0.50.0 customendpoint should expose protocol options")
	}
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	g, ok = edit.GetJSONItem(lm, vscodeGroup)
	if !ok {
		t.Fatalf("missing customendpoint group after upgrade:\n%s", readFile(lm))
	}
	if vscodeModelByID(g, "resp/gpt-5.5").Get("apiType").String() != "responses" {
		t.Errorf("upgraded vscodium resp model apiType = %q", vscodeModelByID(g, "resp/gpt-5.5").Get("apiType").String())
	}
}
