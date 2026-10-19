package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xlog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
)

func TestAccessAuditRotatesWithinBoundAndPreservesRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.jsonl")
	a := &accessAudit{path: path, maxBytes: 600}
	if err := a.open(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if a.file != nil {
			_ = a.file.Close()
		}
	}()
	for i := range 40 {
		err := a.write(auditRecord{At: time.Now().UTC(), User: fmt.Sprint(i), Destination: "example.com", Port: 443, Protocol: "tcp", Status: "accepted"})
		if err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := filepath.Glob(path + "*")
	if len(paths) != auditBackups+1 {
		t.Fatalf("files = %d", len(paths))
	}
	var latest string
	for _, path := range paths {
		info, _ := os.Stat(path)
		if info.Size() > a.maxBytes {
			t.Fatalf("oversized file: %d", info.Size())
		}
		if info.Mode().Perm()&0o007 != 0 {
			t.Fatal("world-readable audit log")
		}
		raw, _ := os.ReadFile(path)
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var row auditRecord
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			if path == a.path {
				latest = row.User
			}
		}
	}
	if latest != "39" {
		t.Fatalf("newest record = %s", latest)
	}
}

func TestAccessAuditQueuesWithoutWaitingAndReportsLoss(t *testing.T) {
	a := &accessAudit{queue: make(chan auditRecord, 1)}
	e := abuse.NewEvent(time.Now(), "alice", "example.com", 443, false, "in >> direct")
	m := &xlog.AccessMessage{From: xnet.TCPDestination(xnet.ParseAddress("198.51.100.7"), 5555)}
	a.offer(e, m)
	a.offer(e, m)
	if a.dropped.Load() != 1 {
		t.Fatal("full audit queue did not count dropped evidence")
	}
	row := <-a.queue
	if row.SourceIP != "198.51.100.7" || row.Outbound != "direct" {
		t.Fatalf("record = %+v", row)
	}
}

// A SOCKS5 relay's hop reaches the exit, which records the same visit under the
// same login; recording it at the relay too would double every piece of evidence.
func TestAccessAuditLeavesSocksRelayHopsToTheExit(t *testing.T) {
	a := &accessAudit{queue: make(chan auditRecord, 2)}
	m := &xlog.AccessMessage{From: xnet.TCPDestination(xnet.ParseAddress("198.51.100.7"), 5555)}
	a.offer(abuse.NewEvent(time.Now(), "alice", "example.com", 443, false, "socks-in >> "+abuse.ChainSocksTag(7, "alice")), m)
	a.offer(abuse.NewEvent(time.Now(), "alice", "example.org", 443, false, "socks-in >> direct"), m)
	if len(a.queue) != 1 {
		t.Fatalf("queued %d records, want only the direct visit", len(a.queue))
	}
	if row := <-a.queue; row.Destination != "example.org" {
		t.Fatalf("record = %+v", row)
	}
}

func TestAccessAuditInvalidPathDoesNotPreventAgentStartup(t *testing.T) {
	a, err := New(Config{StateDir: t.TempDir(), AccessAuditLog: "relative.jsonl"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if a.abuse.audit != nil {
		t.Fatal("invalid audit path was enabled")
	}
}

func TestAgentAccessAuditWorksWithFixedRulesOff(t *testing.T) {
	panel := startPanel(t)
	apiPort, serverPort := freeTCPPort(t), freeTCPPort(t)
	socksPort := startVlessClient(t, serverPort)
	path := filepath.Join(t.TempDir(), "access.jsonl")
	runAgent(t, Config{Master: panel.master, Secret: "s", StateDir: t.TempDir(), AccessAuditLog: path})
	eventually(t, "agent connected", connected(panel.hub))
	applyTo(t, panel, vlessConfig(t, apiPort, serverPort))
	visitPorts(t, socksPort, 2)
	eventually(t, "access audit records without rule matches", func() bool {
		raw, _ := os.ReadFile(path)
		return strings.Count(string(raw), "\n") >= 2
	})
	if len(panel.signals()) != 0 {
		t.Fatal("audit unexpectedly enabled enforcement")
	}
	raw, err := os.ReadFile(path + ".health.json")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	if err := json.Unmarshal(raw, &health); err != nil {
		t.Fatal(err)
	}
	if health["accepted_only"] != true {
		t.Fatalf("health = %s", raw)
	}
}
