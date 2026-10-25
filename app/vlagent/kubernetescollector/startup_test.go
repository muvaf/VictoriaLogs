package kubernetescollector

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestInitRetriesKubernetesAPI(t *testing.T) {
	var discoveryCalls, nodeCalls, podCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var calls *atomic.Int32
		var body string
		switch r.URL.Path {
		case "/api/v1/nodes":
			calls = &discoveryCalls
			body = `{"items":[{"metadata":{"name":"test-node"}}]}`
		case "/api/v1/nodes/test-node":
			calls = &nodeCalls
			body = `{"metadata":{"name":"test-node"}}`
		case "/api/v1/pods":
			calls = &podCalls
			body = `{"items":[],"metadata":{"resourceVersion":"1"}}`
		default:
			t.Errorf("unexpected API request: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "API temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	data := fmt.Sprintf(`{
  "clusters": [{"name": "test", "cluster": {"server": %q}}],
  "users": [{"name": "test", "user": {}}],
  "contexts": [{"name": "test", "context": {"cluster": "test", "user": "test"}}],
  "current-context": "test"
}`, server.URL)
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", config)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	oldEnabled, oldLogsPath, oldCheckpoints := *enabled, *logsPath, *checkpointsPath
	t.Cleanup(func() {
		*enabled, *logsPath, *checkpointsPath = oldEnabled, oldLogsPath, oldCheckpoints
		collector = nil
	})
	*enabled = true
	*logsPath = dir
	*checkpointsPath = filepath.Join(dir, "checkpoints.json")

	Init(dir)
	defer Stop()
	if collector == nil || collector.currentNode.Metadata.Name != "test-node" {
		t.Fatal("collector did not recover from startup API failures")
	}
	if discoveryCalls.Load() != 4 || nodeCalls.Load() != 3 || podCalls.Load() != 2 {
		t.Fatalf("unexpected API attempts: discovery=%d node=%d pods=%d", discoveryCalls.Load(), nodeCalls.Load(), podCalls.Load())
	}
}
