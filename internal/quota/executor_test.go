package quota

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

func TestExecutorBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		hold               bool
		timeout            time.Duration
	}{
		{"timeout", "exec /bin/sleep 5", "timeout", false, 100 * time.Millisecond},
		{"missing-response", "IFS= read -r line\nexit 0", "response_missing", true, 10 * time.Second},
		{"initialize-error", "IFS= read -r line\nprintf '%s\\n' '{\"id\":1,\"error\":{\"code\":401}}'\nIFS= read -r eof\nexit 0", "unauthenticated", true, 10 * time.Second},
		{"partial-frame", "IFS= read -r line\nprintf '%s' '{\"id\":2,\"result\":{}}'\nexec /bin/sleep 5", "timeout", true, 100 * time.Millisecond},
		{"large-output", "exec /usr/bin/head -c 5000000 /dev/zero", "output_too_large", false, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "fake")
			os.WriteFile(binary, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700)
			p := providerquota.QuotaPlan{Binary: binary, Env: []string{"HOME=" + root, "PATH=" + root}, Timeout: tc.timeout, CwdPolicy: providerquota.ScratchCwd, Ready: true, HoldStdinOpen: tc.hold, Stdin: []byte("init\n"), AfterInitialize: []byte("request\n"), Complete: providerquota.RPCComplete(2)}
			_, err := Execute(context.Background(), p, root)
			if providerquota.Reason(err) != tc.reason {
				t.Fatalf("got %v want %s", err, tc.reason)
			}
		})
	}
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	binary := filepath.Join(root, "fake")
	os.WriteFile(binary, []byte("#!/bin/sh\n/bin/touch '"+marker+"'\n"), 0700)
	_, err := Execute(context.Background(), providerquota.QuotaPlan{Binary: binary}, root)
	if providerquota.Reason(err) != "plan_not_ready" {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unready plan launched")
	}
}
