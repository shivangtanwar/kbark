// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shivangtanwar/kbark/internal/incident"
)

func entry(event string) incident.Entry {
	return incident.Entry{Incident: "INC-0007", Cluster: "prod", Namespace: "shop", Workload: "Deployment/cart", Family: "oom", Event: event, Actor: "kbark", Note: "Raise memory limit"}
}

func TestDeliversOnlySelectedEvents(t *testing.T) {
	got := make(chan Message, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer srv.Close()

	w, err := New(srv.URL+"/hooks/secret-token", "https://demo.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Notify(entry("detected")) // not in the default set
	w.Notify(entry("proposed"))

	select {
	case m := <-got:
		if m.Event != "proposed" || m.URL != "https://demo.example/incidents/INC-0007" {
			t.Fatalf("unexpected message: %+v", m)
		}
		for _, want := range []string{"Fix awaiting approval", "deployment/cart", "prod/shop", "Raise memory limit"} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("text lacks %q: %s", want, m.Text)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook delivery")
	}
	select {
	case m := <-got:
		t.Fatalf("unexpected second delivery: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
	if s := w.Status(); s.Sent != 1 || strings.Contains(s.Host, "secret-token") {
		t.Errorf("status: %+v", s)
	}
}

func TestBrokenReceiverIsCountedNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	w, _ := New(srv.URL, "", []string{"proposed"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Notify(entry("proposed"))
	deadline := time.Now().Add(3 * time.Second)
	for w.Status().Failed == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := w.Status(); s.Failed != 1 || s.LastErr == "" {
		t.Fatalf("status: %+v", s)
	}
}

func TestRejectsNonHTTPURL(t *testing.T) {
	if _, err := New("file:///etc/passwd", "", nil); err == nil {
		t.Fatal("want an error")
	}
	var nilHook *Webhook
	if nilHook.Status().Enabled {
		t.Fatal("nil webhook must report disabled")
	}
}
