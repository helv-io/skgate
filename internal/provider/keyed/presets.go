// Package keyed holds the providers that sign in with an API key (or with nothing, for a local server): OpenAI,
// Anthropic, Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama, LM Studio and any OpenAI-compatible endpoint.
// They are data: a preset is a name, a base URL and a hint, and one implementation serves them all.
package keyed

// Preset describes one provider a user can add.
type Preset struct {
	ID   string
	Name string
	Base string // the API base (OpenAI style: it serves /chat/completions and /models)
	// Key says whether the provider needs an API key. Optional means a key may be given but is not required.
	NeedsKey, Optional bool
	Hint               string // where to find the key, or what to run
	Docs               string // a page that explains the API key
	Adapter            string // "anthropic" for the Messages API; empty for OpenAI-compatible
	Custom             bool   // the base URL is entered by the user
}

// Presets is the fixed list, in the order the Add dialog shows it.
var Presets = []Preset{
	{ID: "openai", Name: "OpenAI", Base: "https://api.openai.com/v1", NeedsKey: true, Hint: "Create a key in your OpenAI dashboard (starts with sk-).", Docs: "https://platform.openai.com/api-keys"},
	{ID: "anthropic", Name: "Anthropic", Base: "https://api.anthropic.com/v1", NeedsKey: true, Hint: "Create a key in the Anthropic Console (starts with sk-ant-).", Docs: "https://console.anthropic.com/settings/keys", Adapter: "anthropic"},
	{ID: "gemini", Name: "Google Gemini", Base: "https://generativelanguage.googleapis.com/v1beta/openai", NeedsKey: true, Hint: "Create a key in Google AI Studio.", Docs: "https://aistudio.google.com/apikey"},
	{ID: "mistral", Name: "Mistral", Base: "https://api.mistral.ai/v1", NeedsKey: true, Hint: "Create a key in the Mistral console.", Docs: "https://console.mistral.ai/api-keys"},
	{ID: "deepseek", Name: "DeepSeek", Base: "https://api.deepseek.com/v1", NeedsKey: true, Hint: "Create a key on the DeepSeek platform.", Docs: "https://platform.deepseek.com/api_keys"},
	{ID: "groq", Name: "Groq", Base: "https://api.groq.com/openai/v1", NeedsKey: true, Hint: "Create a key in the Groq console (starts with gsk_).", Docs: "https://console.groq.com/keys"},
	{ID: "openrouter", Name: "OpenRouter", Base: "https://openrouter.ai/api/v1", NeedsKey: true, Hint: "Create a key in your OpenRouter settings (starts with sk-or-).", Docs: "https://openrouter.ai/settings/keys"},
	{ID: "ollama", Name: "Ollama", Base: "http://localhost:11434/v1", Hint: "No key needed. Use an address skgate can reach: from a container that is not localhost.", Docs: "https://ollama.com"},
	{ID: "lmstudio", Name: "LM Studio", Base: "http://localhost:1234/v1", Hint: "No key needed. Start the local server in LM Studio, and use an address skgate can reach.", Docs: "https://lmstudio.ai"},
	{ID: "custom", Name: "Custom (OpenAI-compatible)", Base: "", Optional: true, Custom: true, Hint: "Any endpoint that speaks the OpenAI API: enter its base URL (ending in /v1). The key is optional."},
}

// PresetByID returns the preset with the given id.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
