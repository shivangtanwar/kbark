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
