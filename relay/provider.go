package relay

import "fmt"

// Provider is one OpenAI-compatible upstream in the failover chain. Set BaseURL
// to point at any chat-completions endpoint (OpenAI, OpenRouter, Groq, Cerebras,
// Together, Fireworks, a local vLLM/Ollama, ...). For a handful of well-known
// providers you may set Name alone and the BaseURL is filled in for you.
type Provider struct {
	// Name labels the provider in logs and, for known providers, selects the
	// default BaseURL and quirks. Built-in names: "openai", "openrouter",
	// "groq", "cerebras", "gemini", "together", "fireworks", "deepseek",
	// "mistral". Any other name (e.g. "local") is fine when BaseURL is set
	// explicitly.
	Name string
	// BaseURL is the full chat-completions URL
	// (e.g. https://api.groq.com/openai/v1/chat/completions). Optional when
	// Name is a known provider.
	BaseURL string
	// APIKey is sent as "Authorization: Bearer <APIKey>". Providers with an
	// empty key are dropped from the chain.
	APIKey string
	// Model is the model id forwarded upstream (overrides any model in the
	// client payload, so the key and model never ship in the client).
	Model string
	// NoTrain requests the no-data-collection routing hint. Only honoured by
	// providers that support it (OpenRouter); ignored elsewhere. Must be off
	// for OpenRouter ":free" models, which require data collection.
	NoTrain bool
}

// providerQuirks captures per-provider defaults and protocol differences. They
// all speak the same chat-completions wire format; only the URL and a couple of
// header/body quirks differ.
type providerQuirks struct {
	baseURL string
	// supportsNoTrain is true when the provider honours the OpenRouter-style
	// {"provider":{"data_collection":"deny"}} routing hint.
	supportsNoTrain bool
	// attribution is true when the provider reads HTTP-Referer / X-Title
	// dashboard headers (OpenRouter).
	attribution bool
}

// knownProviders is the registry of built-in upstreams. It exists only to save
// callers from typing a URL for the common cases; any OpenAI-compatible
// endpoint works by setting Provider.BaseURL directly.
var knownProviders = map[string]providerQuirks{
	"openai":     {baseURL: "https://api.openai.com/v1/chat/completions"},
	"openrouter": {baseURL: "https://openrouter.ai/api/v1/chat/completions", supportsNoTrain: true, attribution: true},
	"groq":       {baseURL: "https://api.groq.com/openai/v1/chat/completions"},
	"cerebras":   {baseURL: "https://api.cerebras.ai/v1/chat/completions"},
	"gemini":     {baseURL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
	"together":   {baseURL: "https://api.together.xyz/v1/chat/completions"},
	"fireworks":  {baseURL: "https://api.fireworks.ai/inference/v1/chat/completions"},
	"deepseek":   {baseURL: "https://api.deepseek.com/v1/chat/completions"},
	"mistral":    {baseURL: "https://api.mistral.ai/v1/chat/completions"},
}

// upstream is a fully-resolved provider target (endpoint + quirks + creds).
type upstream struct {
	name        string
	baseURL     string
	apiKey      string
	model       string
	noTrain     bool
	attribution bool
}

// resolve turns a public Provider into an internal upstream, applying built-in
// defaults for known names. It returns an error when the endpoint cannot be
// determined (unknown name and no explicit BaseURL).
func resolve(p Provider) (upstream, error) {
	q, known := knownProviders[p.Name]

	baseURL := p.BaseURL
	if baseURL == "" {
		if !known {
			return upstream{}, fmt.Errorf("provider %q: BaseURL is required for unknown providers", p.Name)
		}
		baseURL = q.baseURL
	}

	return upstream{
		name:        p.Name,
		baseURL:     baseURL,
		apiKey:      p.APIKey,
		model:       p.Model,
		noTrain:     p.NoTrain && known && q.supportsNoTrain,
		attribution: known && q.attribution,
	}, nil
}
