package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/curator-model-router/pkg/routing"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

func fakeEnvironment(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin, "HOME=" + root, "CODEX_HOME=" + filepath.Join(root, "codex-home"), "FAKE_LOG=" + filepath.Join(root, "log"), "CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude-home")}
	return root, env
}
func fakeHarness(t *testing.T, root, runtime string) {
	t.Helper()
	payload, err := os.ReadFile("testdata/" + runtime + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, payload); err != nil {
		t.Fatal(err)
	}
	payload = compact.Bytes()
	path := filepath.Join(root, runtime+".json")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$FAKE_LOG\"\n[ -z \"$(/bin/ls -A .)\" ] || exit 90\n"
	if runtime == "codex" || runtime == "muse" {
		body += "IFS= read -r init || exit 91\nprintf '%s\\n' \"$init\" >> \"$FAKE_LOG\"\n"
		if runtime == "codex" {
			body += "printf '{\"id\":1,\"result\":{\"codexHome\":\"%s\",\"userAgent\":\"codex/1.0.0\"}}\\n' \"$CODEX_HOME\"\n"
		} else {
			body += "printf '%s\\n' '{\"id\":1,\"result\":{}}'\n"
		}
		body += "IFS= read -r initialized || exit 92\nIFS= read -r request || exit 93\nprintf '%s\\n' \"$initialized\" \"$request\" >> \"$FAKE_LOG\"\nprintf '{\"id\":2,\"result\":'\n/bin/cat '" + path + "'\nprintf '}\\n'\nIFS= read -r eof && exit 94\nexit 0\n"
	} else {
		if runtime == "agy" {
			body += "case \"$1\" in --version) printf '%s\\n' 'agy 1.1.27'; exit 0;; --help) printf '%s\\n' '--output-format --mode --print-timeout --print='; exit 0;; esac\n"
		}
		body += "/bin/cat '" + path + "'\n"
	}
	if err = os.WriteFile(filepath.Join(root, "bin", runtime), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestRefreshProviderPlansAndTTL(t *testing.T) {
	for _, runtime := range Runtimes {
		t.Run(runtime, func(t *testing.T) {
			root, env := fakeEnvironment(t)
			fakeHarness(t, root, runtime)
			now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
			first, err := Refresh(context.Background(), root, runtime, env, now, 10*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if first.State == providerquota.Unavailable || len(first.Windows) == 0 {
				t.Fatalf("record: %+v", first)
			}
			logBefore, err := os.ReadFile(filepath.Join(root, "log"))
			if err != nil {
				t.Fatal(err)
			}
			second, err := Refresh(context.Background(), root, runtime, env, now.Add(time.Minute), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			logAfter, _ := os.ReadFile(filepath.Join(root, "log"))
			if string(logAfter) != string(logBefore) || !second.RetrievedAt.Equal(first.RetrievedAt) {
				t.Fatal("TTL re-executed")
			}
			if runtime == "codex" && !strings.Contains(string(logBefore), "account/rateLimits/read") {
				t.Fatal("missing codex read")
			}
			if runtime == "muse" && !strings.Contains(string(logBefore), "usage/read") {
				t.Fatal("missing muse read")
			}
			if runtime == "claude" && !strings.Contains(string(logBefore), "--no-session-persistence") {
				t.Fatal("missing read-only flags")
			}
			if runtime == "agy" && !strings.Contains(string(logBefore), "--print=/usage") {
				t.Fatal("missing agy read")
			}
			records, err := Read(root, env, now.Add(time.Minute))
			if err != nil || len(records) != 1 {
				t.Fatal(records, err)
			}
			catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := Project(records, catalog, now.Add(time.Minute))
			if len(snapshot.Facts) != 1 || snapshot.AsOf != now.Add(time.Minute).Unix() {
				t.Fatal(snapshot)
			}
			// Provider JSON never exposes harness homes or raw output to routing.
			b, _ := json.Marshal(snapshot)
			if strings.Contains(string(b), root) {
				t.Fatalf("home leaked %s", b)
			}
			expired, err := Refresh(context.Background(), root, runtime, env, now.Add(11*time.Minute), 10*time.Minute)
			if err != nil || expired.RetrievedAt.Equal(first.RetrievedAt) {
				t.Fatal("stale not refreshed", err)
			}
		})
	}
}
func TestConcurrentRefreshLock(t *testing.T) {
	root, env := fakeEnvironment(t)
	fakeHarness(t, root, "codex")
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Refresh(context.Background(), root, "codex", env, now, 10*time.Minute)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, providerquota.ErrLocked) {
			t.Fatal(err)
		}
	}
	log, _ := os.ReadFile(filepath.Join(root, "log"))
	if strings.Count(string(log), "account/rateLimits/read") != 1 {
		t.Fatalf("duplicate reads: %s", log)
	}
}
func TestFailedRefreshRetainsMeasurementAge(t *testing.T) {
	root, env := fakeEnvironment(t)
	fakeHarness(t, root, "codex")
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	first, err := Refresh(context.Background(), root, "codex", env, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "bin", "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700)
	failed, err := Refresh(context.Background(), root, "codex", env, now.Add(2*time.Second), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != providerquota.Unavailable || failed.WindowsDigest != first.WindowsDigest || first.ObservedAt == nil || failed.ObservedAt == nil || !failed.ObservedAt.Equal(*first.ObservedAt) {
		t.Fatal(failed)
	}
}
func TestProjectionWindowFreshness(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	old := now.Add(-11 * time.Minute)
	reset := now.Add(time.Hour)
	r := providerquota.QuotaRecord{Key: "runtime-codex", Runtime: "codex", State: providerquota.Exact, TTLS: 600, Windows: []providerquota.QuotaWindow{
		{ID: "limit", Kind: "primary", Minutes: 300, UsedPercent: providerquota.Float(12.345), ObservedAt: &now, ResetsAt: &reset},
		{ID: "limit", Kind: "secondary", Minutes: 10080, UsedPercent: providerquota.Float(80), ObservedAt: &old, ResetsAt: &reset},
		{ID: "unknown", Minutes: 300, UsedPercent: providerquota.Float(0)},
	}}
	out := Project([]providerquota.QuotaRecord{r}, recommend.Catalog{}, now)
	if len(out.Facts[0].Windows) != 3 || out.Facts[0].Windows[0].ID == out.Facts[0].Windows[1].ID {
		t.Fatal(out)
	}
	ws := out.Facts[0].Windows
	if *ws[0].UsedBP != 1234 || ws[0].Freshness != routing.Fresh || ws[1].Freshness != routing.Stale || ws[2].Freshness != routing.Invalid {
		t.Fatal(ws)
	}
}

func TestProjectedAccountQuotaStopsRecommendation(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	record := providerquota.QuotaRecord{Key: "runtime-codex", Runtime: "codex", State: providerquota.Exact, TTLS: 600, Windows: []providerquota.QuotaWindow{
		{ID: "codex", Kind: "primary", Minutes: 10080, UsedPercent: providerquota.Float(75), ObservedAt: &now, ResetsAt: &reset},
	}}
	usage := Project([]providerquota.QuotaRecord{record}, catalog, now)
	if usage.Facts[0].Windows[0].Scope != "all" {
		t.Fatal("account-wide quota lost its scope", usage)
	}
	policy := recommend.DefaultPolicy()
	stop := int64(2500)
	policy.Rules = []recommend.Rule{{ID: "weekly-stop", Source: "operator ruling: quota stop (example)", QuotaStopBP: &stop}}
	candidates := []recommend.Candidate{}
	for _, row := range catalog.Rows {
		if row.Runtime == "codex" {
			candidates = append(candidates, row.Candidate)
		}
	}
	result, err := recommend.Recommend(recommend.Request{Catalog: catalog, Policy: policy, Candidates: candidates, Usage: usage,
		Task: recommend.TaskProfile{Role: "developer", TaskClass: "code.implement"}, AdmissionSource: "candidates-file"})
	if err != nil || result.Refusal == nil || result.Refusal.Code != recommend.NoQualifiedCandidate {
		t.Fatalf("account-wide stop did not reach recommendation: %+v, %v", result, err)
	}
}
