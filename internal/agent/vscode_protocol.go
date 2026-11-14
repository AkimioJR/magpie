package agent

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

const (
	vscodeAPIChat      = "chat-completions"
	vscodeAPIResponses = "responses"
	vscodeAPIMessages  = "messages"
)

var vscodeProtocolOrder = []string{
	vscodeAPIChat,
	vscodeAPIResponses,
	vscodeAPIMessages,
}

var vscodeProtocolNames = map[string]string{
	vscodeAPIChat:      "OpenAI Chat Completions",
	vscodeAPIResponses: "OpenAI Responses",
	vscodeAPIMessages:  "Anthropic Messages",
}

func vscodeProtocolKey(id string) string   { return "vscode:" + id + ":protocol" }
func vscodeAPIsWantedKey(lm string) string { return "vscode:" + lm + ":apis" }

// vscodeProtocol is the user's explicit protocol override for k, or "" for auto.
func vscodeProtocol(id string) string {
	v := stashShared()[vscodeProtocolKey(id)]
	if _, ok := vscodeProtocolNames[v]; ok {
		return v
	}
	return ""
}

// vscodeAPIForModel picks the best Copilot Custom Endpoint apiType for m from
// the APIs its provider serves: Responses > Messages > Chat Completions.
func vscodeAPIForModel(m catalog.Model) string {
	switch {
	case slices.Contains(m.APIs, string(provider.Responses)):
		return vscodeAPIResponses
	case slices.Contains(m.APIs, string(provider.Anthropic)):
		return vscodeAPIMessages
	default:
		return vscodeAPIChat
	}
}

func vscodeModelURL(apiType string) string {
	switch apiType {
	case vscodeAPIResponses:
		return gatewayV1() + "/responses"
	case vscodeAPIMessages:
		return gatewayV1() + "/messages"
	default:
		return vscodeURL()
	}
}

// vscodeCheckURL returns the gateway endpoint URL expected on models.0 in group g.
func vscodeCheckURL(g, vendor string) string {
	if vendor == vscodeOAI {
		return vscodeURL()
	}
	api := gjson.Get(g, "models.0.apiType").String()
	if api == "" {
		api = gjson.Get(g, "apiType").String()
	}
	return vscodeModelURL(api)
}

type vscodeAPIsRecord struct {
	Group  string              `json:"group,omitempty"`
	Models map[string][]string `json:"models,omitempty"`
}

func loadVSCodeAPIsRecord(lm string) (vscodeAPIsRecord, bool) {
	raw, ok := stashShared()[vscodeAPIsWantedKey(lm)]
	if !ok || raw == "" {
		return vscodeAPIsRecord{}, false
	}
	var rec vscodeAPIsRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return vscodeAPIsRecord{}, false
	}
	return rec, true
}

func saveVSCodeAPIsRecord(lm string, rec vscodeAPIsRecord) {
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	k := vscodeAPIsWantedKey(lm)
	s := string(b)
	if stashShared()[k] == s {
		return
	}
	stash(map[string]string{k: s})
}

func vscodeModelBaseEntry(k vscodeKind, m catalog.Model, vendor string) (map[string]any, int) {
	context := m.Context
	if context == 0 {
		context = unknownWindow(128000) // what VS Code takes a model of unknown window for
	}
	// VS Code keeps the output budget out of the window it fills with
	// the prompt, so a model whose output limit is near its window
	// would leave the prompt next to no room
	output := maxTokens(m)
	if output <= 0 {
		output = 8192
	}
	output = outputOr(m, min(output, context/4))
	entry := map[string]any{
		"id": m.ID, "name": m.Name, "url": vscodeURL(),
		"toolCalling": true, "vision": m.Images,
		"contextWindow": context, "maxOutputTokens": output,
		"requestHeaders": map[string]string{"Authorization": "Bearer " + k.token()},
	}
	if vendor == vscodeOAI {
		// its window is maxInputTokens plus maxOutputTokens, and an
		// Authorization of the model's own is dropped
		delete(entry, "contextWindow")
		entry["maxInputTokens"] = context - output
		entry["requestHeaders"] = map[string]string{"x-api-key": k.token()}
	}
	return entry, output
}

func vscodeApplyModelProtocol(entry map[string]any, m catalog.Model, output int, modelAPI, effAPI string) {
	if effAPI == "" {
		effAPI = vscodeAPIChat
	}
	if modelAPI != "" {
		entry["apiType"] = modelAPI
	} else {
		delete(entry, "apiType")
	}
	entry["url"] = vscodeModelURL(effAPI)

	delete(entry, "thinking")
	delete(entry, "adaptiveThinking")
	delete(entry, "minThinkingBudget")
	delete(entry, "maxThinkingBudget")

	if m.Reasoning || len(m.Efforts) > 0 {
		entry["thinking"] = true
		if effAPI == vscodeAPIMessages {
			if gateway.AdaptiveThinking(m.ID) || gateway.AdaptiveWithEffort(m.ID) {
				entry["adaptiveThinking"] = true
			} else if output > 1024 {
				entry["minThinkingBudget"] = 1024
				entry["maxThinkingBudget"] = min(32000, output-1)
			}
		}
	}
	if len(m.Efforts) > 0 {
		entry["supportsReasoningEffort"] = m.Efforts
		entry["reasoningEffortFormat"] = effAPI
	}
}

func vscodeBuildEndpointGroup(k vscodeKind, catalogModels []catalog.Model, lm, cur string, hasCur, force bool) (map[string]any, vscodeAPIsRecord) {
	override := vscodeProtocol(k.id)
	wantGroupAPI := vscodeAPIChat
	if override != "" {
		wantGroupAPI = override
	}

	effGroupAPI := wantGroupAPI
	recGroupToSave := wantGroupAPI
	groupEdited := false

	var rec vscodeAPIsRecord
	var hasRec bool
	curModels := map[string]gjson.Result{}
	if hasCur && !force {
		rec, hasRec = loadVSCodeAPIsRecord(lm)
		fGroupAPI := gjson.Get(cur, "apiType").String()
		if fGroupAPI != "" {
			if hasRec && rec.Group != "" {
				groupEdited = fGroupAPI != rec.Group
			} else {
				groupEdited = fGroupAPI != wantGroupAPI
			}
		}
		if groupEdited {
			effGroupAPI = fGroupAPI
			if rec.Group != "" {
				recGroupToSave = rec.Group
			} else {
				recGroupToSave = wantGroupAPI
			}
		}
		for _, cm := range gjson.Get(cur, "models").Array() {
			curModels[cm.Get("id").String()] = cm
		}
	}

	list := make([]any, 0, len(catalogModels))
	nextModelsRec := make(map[string][]string, len(catalogModels))
	for _, m := range catalogModels {
		wantAPI := vscodeAPIForModel(m)
		if override != "" {
			wantAPI = override
		}
		modelAPI := wantAPI
		effAPI := wantAPI
		nextRec := []string{wantAPI}

		if hasCur && !force {
			if cm, exists := curModels[m.ID]; exists {
				fAPI := cm.Get("apiType").String()
				prev, hasPrev := rec.Models[m.ID]
				switch {
				case fAPI == "":
					// User removed per-model apiType to inherit group, or edited legacy group apiType:
					// preserve inheritance unless this is a normal upgrade from legacy magpie.
					if hasPrev || groupEdited {
						modelAPI = ""
						effAPI = effGroupAPI
						if hasPrev {
							nextRec = prev
						}
					}
				case (hasPrev && !slices.Contains(prev, fAPI)) || (!hasPrev && fAPI != wantAPI):
					// User hand-edited per-model apiType in chatLanguageModels.json: keep it until overridden.
					modelAPI = fAPI
					effAPI = fAPI
					if hasPrev {
						nextRec = prev
					}
				}
			}
		}

		entry, output := vscodeModelBaseEntry(k, m, vscodeEndpoint)
		vscodeApplyModelProtocol(entry, m, output, modelAPI, effAPI)
		list = append(list, entry)
		nextModelsRec[m.ID] = nextRec
	}

	return map[string]any{
		"name":    magpieID,
		"vendor":  vscodeEndpoint,
		"apiType": effGroupAPI,
		"models":  list,
	}, vscodeAPIsRecord{Group: recGroupToSave, Models: nextModelsRec}
}

func vscodeBuildOAIGroup(k vscodeKind, catalogModels []catalog.Model) map[string]any {
	list := make([]any, 0, len(catalogModels))
	for _, m := range catalogModels {
		entry, _ := vscodeModelBaseEntry(k, m, vscodeOAI)
		if len(m.Efforts) > 0 {
			entry["supportsReasoningEffort"] = m.Efforts
			entry["reasoningEffortFormat"] = vscodeAPIChat
		}
		list = append(list, entry)
	}
	return map[string]any{"name": magpieID, "vendor": vscodeOAI, "models": list}
}

func vscodeSetGroup(k vscodeKind, models string, lms []string, force bool) error {
	vendor := k.chat().vendor
	catalogModels := sharedModels(k.id)
	for _, lm := range append([]string{models}, lms...) {
		for _, other := range vscodeVendors {
			if other != vendor {
				if err := edit.DelJSONItem(lm, vscodeGroupOf(other)); err != nil {
					return err
				}
			}
		}
		cur, hasCur := edit.GetJSONItem(lm, vscodeGroupOf(vendor))
		if vendor == vscodeOAI {
			forget(vscodeAPIsWantedKey(lm))
			v := vscodeBuildOAIGroup(k, catalogModels)
			var want any = v
			if hasCur {
				if want = vscodeGroupKept(cur, v); sameJSON(cur, want) {
					continue
				}
			}
			if err := edit.SetJSONItem(lm, vscodeGroupOf(vendor), want); err != nil {
				return err
			}
			continue
		}

		v, rec := vscodeBuildEndpointGroup(k, catalogModels, lm, cur, hasCur, force)
		var want any = v
		if hasCur {
			want = vscodeGroupKept(cur, v)
		}
		if hasCur && sameJSON(cur, want) {
			saveVSCodeAPIsRecord(lm, rec)
			continue
		}
		if err := edit.SetJSONItem(lm, vscodeGroupOf(vendor), want); err != nil {
			return err
		}
		saveVSCodeAPIsRecord(lm, rec)
	}
	return nil
}

func vscodeProtocolField(k vscodeKind, joined func() bool, setGroup func([]string, bool) error, profiles func(func(settings, lms []string) error) error) Field {
	return Field{
		Key:   "protocol",
		Label: "protocol",
		Quiet: true,
		Get:   func() string { return vscodeProtocol(k.id) },
		Set: func(v string) error {
			if v != "" {
				if _, ok := vscodeProtocolNames[v]; !ok {
					return fmt.Errorf("%s: unknown protocol %q", k.name, v)
				}
			}
			if v == "" {
				forget(vscodeProtocolKey(k.id))
			} else {
				stash(map[string]string{vscodeProtocolKey(k.id): v})
			}
			if !joined() {
				return nil
			}
			return profiles(func(_, lms []string) error { return setGroup(lms, true) })
		},
		Options: func(map[string]string) []Option {
			if k.chat().vendor == vscodeOAI {
				return nil
			}
			out := []Option{{Value: "", Label: "Each model's own", Note: "each model on the API its provider serves"}}
			for _, p := range vscodeProtocolOrder {
				out = append(out, Option{Value: p, Label: vscodeProtocolNames[p], Note: p})
			}
			return out
		},
	}
}
