package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunTaskFreshFallbackWaitsForAuthorityReset(t *testing.T) {
	for _, acknowledged := range []bool{true, false} {
		name := "acknowledged"
		if !acknowledged {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			d, argsFile, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			first := leaderReuseTestTask("retained-first")
			first.IsLeaderTask = false
			ackFile := filepath.Join(t.TempDir(), "server-reset-ack")
			// The resumed invocation is positively rejected without tools. The
			// next cold invocation refuses to run unless the server has already
			// acknowledged invalidation, making the ordering observable.
			script := `#!/bin/sh
printf '%s\n' "$@" >> "` + argsFile + `"
printf '%s\n' '--invocation-end--' >> "` + argsFile + `"
for arg in "$@"; do
  if [ "$arg" = "--resume" ]; then
    printf '%s\n' 'No conversation found with session ID' >&2
    exit 1
  fi
done
if [ ! -f "` + ackFile + `" ]; then
  printf '%s\n' 'fresh prompt executed before server reset acknowledgement' >&2
  exit 1
fi
IFS= read -r _
printf '%s\n' '{"type":"system","session_id":"fresh-fallback-session"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"fresh-fallback-session","result":"done"}'
`
			second := leaderReuseTestTask("retained-second")
			second.IsLeaderTask = false
			second.DispatchedAt = "2026-09-26T10:11:12.123456Z"
			var resets atomic.Int32
			freshPin := make(chan bool, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/daemon/tasks/"+second.ID+"/session" {
					var pin struct {
						SessionID       string `json:"session_id"`
						AfterFreshReset bool   `json:"after_fresh_reset"`
					}
					if err := json.NewDecoder(r.Body).Decode(&pin); err != nil {
						t.Error(err)
					}
					if pin.SessionID == "fresh-fallback-session" {
						freshPin <- pin.AfterFreshReset
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path != "/api/daemon/tasks/"+second.ID+"/session/fresh" {
					w.WriteHeader(http.StatusOK)
					return
				}
				resets.Add(1)
				var req protocol.FreshTaskSessionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RuntimeID != second.RuntimeID || req.DispatchedAt != second.DispatchedAt || r.Method != http.MethodPost {
					t.Errorf("reset request was not the exact running attempt: req=%+v err=%v method=%s", req, err, r.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if !acknowledged {
					w.WriteHeader(http.StatusConflict)
					return
				}
				if err := os.WriteFile(ackFile, []byte("ack"), 0o600); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			d.client = NewClient(srv.URL)
			d.cfg.ServerBaseURL = srv.URL
			// Configure the client before either execution starts: session pins
			// intentionally outlive the drain and must share a stable client.
			prior, err := d.runTask(context.Background(), first, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			second.PriorSessionID, second.PriorWorkDir = prior.SessionID, prior.WorkDir
			writeTestExecutable(t, d.cfg.Agents["claude"].Path, []byte(script))
			result, err := d.runTask(context.Background(), second, "claude", 0, d.logger)
			if acknowledged {
				if err != nil || result.SessionID != "fresh-fallback-session" {
					t.Fatalf("acknowledged fresh fallback failed: result=%+v err=%v", result, err)
				}
				select {
				case phase := <-freshPin:
					if !phase {
						t.Fatal("fresh execution did not pin with its acknowledged reset phase")
					}
				case <-time.After(time.Second):
					t.Fatal("fresh execution never pinned its crash recovery session")
				}
			} else if err == nil || !strings.Contains(err.Error(), "invalidate retained context before fresh session retry") {
				t.Fatalf("failed authority reset did not stop fresh execution: result=%+v err=%v", result, err)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			wantInvocations := 2 // original task, then rejected retained attempt
			if acknowledged {
				wantInvocations++
			}
			if got := strings.Count(string(args), "--invocation-end--"); got != wantInvocations || resets.Load() != 1 {
				t.Fatalf("fallback ran before a successful reset: invocations=%d want=%d resets=%d args=%s", got, wantInvocations, resets.Load(), args)
			}
		})
	}
}
