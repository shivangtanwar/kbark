// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const providerPingTimeout = 3 * time.Second

func checkAnthropic(ctx context.Context) Result {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return Result{Name: "anthropic", Status: Red, Detail: "ANTHROPIC_API_KEY unset"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, providerPingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return Result{Name: "anthropic", Status: Yellow, Detail: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	return classifyHTTP("anthropic", resp, err)
}

func checkOpenAI(ctx context.Context) Result {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return Result{Name: "openai", Status: Red, Detail: "OPENAI_API_KEY unset"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, providerPingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		return Result{Name: "openai", Status: Yellow, Detail: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	return classifyHTTP("openai", resp, err)
}

func checkOllama(ctx context.Context) Result {
	host := os.Getenv("OLLAMA_HOST")
	hostSetExplicitly := host != ""
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	pingCtx, cancel := context.WithTimeout(ctx, providerPingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		return Result{Name: "ollama", Status: Yellow, Detail: fmt.Sprintf("build request: %v", err)}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Local daemon not running is RED only if the user explicitly set OLLAMA_HOST;
		// otherwise YELLOW since "local model not started" is a routine state.
		if hostSetExplicitly {
			return Result{Name: "ollama", Status: Red, Detail: err.Error()}
		}
		return Result{Name: "ollama", Status: Yellow, Detail: "no daemon at " + host}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return Result{Name: "ollama", Status: Green, Detail: host}
	}
	return Result{Name: "ollama", Status: Yellow, Detail: fmt.Sprintf("HTTP %d from %s", resp.StatusCode, host)}
}

// classifyHTTP maps an HTTP outcome to a Result. Auth failures are RED so a
// stale or wrong key surfaces; transient upstream / network issues are YELLOW
// so a healthy provider doesn't get falsely flagged.
func classifyHTTP(name string, resp *http.Response, err error) Result {
	if err != nil {
		return Result{Name: name, Status: Yellow, Detail: err.Error()}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return Result{Name: name, Status: Green, Detail: "reachable"}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return Result{Name: name, Status: Red, Detail: fmt.Sprintf("HTTP %d (auth)", resp.StatusCode)}
	case resp.StatusCode >= 500:
		return Result{Name: name, Status: Yellow, Detail: fmt.Sprintf("HTTP %d (upstream)", resp.StatusCode)}
	default:
		return Result{Name: name, Status: Yellow, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
}
