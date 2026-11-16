// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/server"
)

// The incidents commands are a terminal client for a running
// `kbark serve`. They go through the same HTTP API as the dashboard,
// so the same rule holds: a write needs a named operator.

var (
	incidentsServer   string
	incidentsOperator string
	incidentsAll      bool
	incidentsReason   string
	incidentsUser     string
	apiClient         *http.Client
)

var incidentsCmd = &cobra.Command{
	Use:   "incidents",
	Short: "List and act on incidents from a running `kbark serve`",
	RunE:  func(cmd *cobra.Command, _ []string) error { return listIncidents(cmd) },
}

func init() {
	pf := incidentsCmd.PersistentFlags()
	pf.StringVar(&incidentsServer, "server", envOr("KBARK_SERVER", "http://127.0.0.1:8765"), "Address of the kbark dashboard")
	pf.StringVar(&incidentsOperator, "as", defaultOperator(), "Your name, recorded in the audit log (ignored when the server requires sign-in)")
	pf.StringVar(&incidentsUser, "user", os.Getenv("KBARK_USER"), "Username, when the server requires sign-in; the password comes from $KBARK_PASSWORD or a prompt")
	incidentsCmd.Flags().BoolVarP(&incidentsAll, "all", "a", false, "Include closed incidents")

	show := &cobra.Command{
		Use: "show <id>", Short: "Show evidence, explanation and the proposed change", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var inc incident.Incident
			if err := apiCall(http.MethodGet, "/api/incidents/"+args[0], nil, &inc); err != nil {
				return err
			}
			printIncident(cmd.OutOrStdout(), &inc)
			return nil
		},
	}
	verb := func(name, short, done string) *cobra.Command {
		return &cobra.Command{
			Use: name + " <id>", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var inc incident.Incident
				body := map[string]string{"operator": incidentsOperator, "reason": incidentsReason}
				if err := apiCall(http.MethodPost, "/api/incidents/"+args[0]+"/"+name, body, &inc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s. State: %s\n", inc.ID, done, inc.State)
				return nil
			},
		}
	}
	reject := verb("reject", "Decline the proposed fix; nothing is changed", "rejected")
	reject.Flags().StringVar(&incidentsReason, "reason", "", "Why the fix is declined")
	incidentsCmd.AddCommand(
		show,
		verb("approve", "Apply the proposed fix and start verification", "approved"),
		reject,
		verb("rollback", "Undo an applied fix", "rolled back"),
	)
	rootCmd.AddCommand(incidentsCmd)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultOperator() string {
	if v := os.Getenv("KBARK_OPERATOR"); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// client returns an HTTP client, signing in first when a username was
// given. The session cookie lives only for this process.
func client() (*http.Client, error) {
	if apiClient != nil {
		return apiClient, nil
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Timeout: 60 * time.Second, Jar: jar}
	if incidentsUser != "" {
		password := os.Getenv("KBARK_PASSWORD")
		if password == "" {
			var err error
			if password, err = readPassword("Password for " + incidentsUser + ": "); err != nil {
				return nil, err
			}
		}
		raw, _ := json.Marshal(map[string]string{"username": incidentsUser, "password": password})
		if err := do(c, http.MethodPost, "/api/login", raw, nil); err != nil {
			return nil, fmt.Errorf("sign in: %w", err)
		}
	}
	apiClient = c
	return c, nil
}

func apiCall(method, path string, body, out any) error {
	c, err := client()
	if err != nil {
		return err
	}
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	err = do(c, method, path, raw, out)
	if err != nil && err.Error() == "sign in required" && incidentsUser == "" {
		return errors.New("this server requires sign-in: pass --user (password from $KBARK_PASSWORD or a prompt)")
	}
	return err
}

func do(c *http.Client, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(incidentsServer, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach kbark at %s (is `kbark serve` running?): %w", incidentsServer, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("kbark answered %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func listIncidents(cmd *cobra.Command) error {
	var st server.State
	if err := apiCall(http.MethodGet, "/api/state", nil, &st); err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tCLUSTER\tNAMESPACE\tWORKLOAD\tFAILURE\tPROPOSED FIX")
	n := 0
	for _, inc := range st.Incidents {
		if inc.ClosedAt != nil && !incidentsAll {
			continue
		}
		fix := "none (needs a person)"
		if inc.Action != nil {
			fix = inc.Action.Title
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", inc.ID, inc.State, inc.Cluster, inc.Namespace, strings.ToLower(inc.Workload.String()), inc.Family, fix)
		n++
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if n == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No open incidents.")
	}
	return nil
}

func printIncident(w io.Writer, inc *incident.Incident) {
	fmt.Fprintf(w, "%s  %s  [%s]\n", inc.ID, strings.ToLower(inc.Workload.String()), inc.State)
	fmt.Fprintf(w, "%s / %s\n%s\n\nObserved:\n", inc.Cluster, inc.Namespace, inc.Summary)
	for _, e := range inc.Evidence {
		fmt.Fprintf(w, "  %-18s %s\n", e.Label+":", e.Value)
	}
	if inc.Explanation != "" {
		fmt.Fprintf(w, "\nExplanation (%s, advice only):\n%s\n", inc.ExplanationNote, inc.Explanation)
	}
	switch {
	case inc.Action != nil:
		a := inc.Action
		fmt.Fprintf(w, "\nChange: %s\n%s\nRisk: %s. Reversible: %t.\n", a.Title, a.Rationale, a.Risk, a.Reversible)
		for _, warn := range a.Warnings {
			fmt.Fprintf(w, "Check first: %s\n", warn)
		}
		fmt.Fprintf(w, "\n%s\n", a.Diff)
	case inc.ManualReason != "":
		fmt.Fprintf(w, "\nNo change proposed: %s\n", inc.ManualReason)
	}
	fmt.Fprintln(w, "History:")
	for _, h := range inc.History {
		fmt.Fprintf(w, "  %s  %-12s %s\n", h.Time.Local().Format("15:04:05"), h.Actor, h.Note)
	}
}
