// SPDX-License-Identifier: Apache-2.0

package ai

import "fmt"

// New returns a Provider by canonical name. Providers are constructed
// lazily so missing env vars only error on demand, not at startup.
func New(name string) (Provider, error) {
	switch name {
	case "anthropic":
		return NewAnthropic()
	case "openai":
		return NewOpenAI()
	case "ollama":
		return NewOllama()
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: anthropic, openai, ollama)", name)
	}
}

// Endpoint describes where and how to reach a provider. BaseURL and
// APIKeyEnv are optional and apply to the openai provider only: they
// point it at an OpenAI-compatible endpoint with its own key variable.
type Endpoint struct {
	Provider  string
	BaseURL   string
	APIKeyEnv string
}

// NewEndpoint is New with optional endpoint overrides.
func NewEndpoint(e Endpoint) (Provider, error) {
	if e.BaseURL == "" && e.APIKeyEnv == "" {
		return New(e.Provider)
	}
	if e.Provider != "openai" {
		return nil, fmt.Errorf("base_url and api_key_env are supported for provider \"openai\" only, not %q", e.Provider)
	}
	return NewOpenAICompatible(e.BaseURL, e.APIKeyEnv)
}
