// SPDX-License-Identifier: Apache-2.0

// Package notify posts incident events to a webhook so people learn a
// fix is waiting without watching the dashboard. The payload carries a
// "text" field, which Slack, Mattermost and compatible receivers render
// directly, plus structured fields for anything else.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shivangtanwar/kbark/internal/incident"
)

// DefaultEvents are the audit events worth interrupting someone for.
var DefaultEvents = []string{"proposed", "manual", "failed", "verified", "rolled-back"}

// Message is the webhook body.
type Message struct {
	Text      string `json:"text"`
	Event     string `json:"event"`
	Incident  string `json:"incident"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Family    string `json:"family"`
	Actor     string `json:"actor"`
	Note      string `json:"note,omitempty"`
	URL       string `json:"url,omitempty"`
}

// Webhook delivers events asynchronously. A slow or broken receiver
// never delays detection or fixes: the queue is bounded and overflow is
// dropped and counted.
type Webhook struct {
	url       string
	publicURL string
	events    map[string]bool
	queue     chan incident.Entry
	client    *http.Client

	sent, failed, dropped atomic.Int64
	lastErr               atomic.Value
}

// New builds a webhook notifier. publicURL, when set, is the
// dashboard's address and is used to link to the incident.
func New(webhookURL, publicURL string, events []string) (*Webhook, error) {
	u, err := url.Parse(webhookURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("notify webhook: %q is not an http(s) URL", webhookURL)
	}
	if len(events) == 0 {
		events = DefaultEvents
	}
	w := &Webhook{
		url: webhookURL, publicURL: strings.TrimRight(publicURL, "/"), events: map[string]bool{},
		queue: make(chan incident.Entry, 256), client: &http.Client{Timeout: 10 * time.Second},
	}
	for _, e := range events {
		w.events[strings.TrimSpace(e)] = true
	}
	return w, nil
}

// Notify queues an event. Never blocks.
func (w *Webhook) Notify(e incident.Entry) {
	if !w.events[e.Event] {
		return
	}
	select {
	case w.queue <- e:
	default:
		w.dropped.Add(1)
	}
}

// Run delivers queued events until ctx ends.
func (w *Webhook) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-w.queue:
			if err := w.post(ctx, e); err != nil {
				w.failed.Add(1)
				w.lastErr.Store(err.Error())
			} else {
				w.sent.Add(1)
			}
		}
	}
}

var headlines = map[string]string{
	"proposed":    "Fix awaiting approval",
	"manual":      "Needs a person",
	"failed":      "Fix did not work",
	"verified":    "Fixed and verified",
	"rolled-back": "Fix rolled back",
	"rejected":    "Fix rejected",
	"resolved":    "Cleared on its own",
	"detected":    "Failure detected",
	"applied":     "Fix applied",
	"applying":    "Fix approved",
}

// Build renders the message for an event.
func (w *Webhook) Build(e incident.Entry) Message {
	head := headlines[e.Event]
	if head == "" {
		head = e.Event
	}
	m := Message{
		Event: e.Event, Incident: e.Incident, Cluster: e.Cluster, Namespace: e.Namespace,
		Workload: e.Workload, Family: e.Family, Actor: e.Actor, Note: e.Note,
	}
	if w.publicURL != "" {
		m.URL = w.publicURL + "/incidents/" + e.Incident
	}
	m.Text = fmt.Sprintf("[kbark] %s: %s in %s/%s (%s)", head, strings.ToLower(e.Workload), e.Cluster, e.Namespace, e.Incident)
	if e.Note != "" {
		m.Text += "\n" + e.Note
	}
	if m.URL != "" {
		m.Text += "\n" + m.URL
	}
	return m
}

func (w *Webhook) post(ctx context.Context, e incident.Entry) error {
	body, _ := json.Marshal(w.Build(e))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %s", res.Status)
	}
	return nil
}

// Status summarises delivery for the settings page. The webhook URL
// often embeds a secret, so only its host is reported.
type Status struct {
	Enabled bool     `json:"enabled"`
	Host    string   `json:"host,omitempty"`
	Events  []string `json:"events,omitempty"`
	Sent    int64    `json:"sent"`
	Failed  int64    `json:"failed"`
	Dropped int64    `json:"dropped"`
	LastErr string   `json:"lastError,omitempty"`
}

// Status reports delivery counters. Safe on a nil receiver.
func (w *Webhook) Status() Status {
	if w == nil {
		return Status{}
	}
	u, _ := url.Parse(w.url)
	s := Status{Enabled: true, Host: u.Host, Sent: w.sent.Load(), Failed: w.failed.Load(), Dropped: w.dropped.Load()}
	for e := range w.events {
		s.Events = append(s.Events, e)
	}
	if v, ok := w.lastErr.Load().(string); ok {
		s.LastErr = v
	}
	return s
}
