package quota

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/relux-works/skill-agents-management/pkg/agentic"
	"github.com/relux-works/skill-agents-management/pkg/agentic/systems/agy"
	"github.com/relux-works/skill-agents-management/pkg/agentic/systems/claude"
	"github.com/relux-works/skill-agents-management/pkg/agentic/systems/codex"
	"github.com/relux-works/skill-agents-management/pkg/agentic/systems/muse"
	"github.com/relux-works/skill-agents-management/pkg/providerlimits"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

var Runtimes = []string{"codex", "claude", "muse", "agy"}

func System(runtime string) (agentic.System, error) {
	switch runtime {
	case "codex":
		return codex.New(), nil
	case "claude":
		return claude.New(), nil
	case "muse":
		return muse.New(), nil
	case "agy":
		return agy.New(), nil
	default:
		return nil, providerquota.Refuse("runtime_invalid")
	}
}
func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], key+"=") {
			return strings.TrimPrefix(env[i], key+"=")
		}
	}
	return ""
}

// ParseContext uses only lexical plugin/environment evidence. It never stats,
// opens, canonicalizes symlinks in, or lists a harness home.
func ParseContext(s agentic.System, env []string, now time.Time) (providerquota.ParseContext, error) {
	c := providerquota.ParseContext{Runtime: string(s.ID()), ReadAt: now, RetrievedAt: now}
	caps := s.Capabilities()
	home := envValue(env, caps.HomeEnvVar)
	if home == "" {
		home = caps.DefaultHome
	}
	if home == "" {
		return c, nil
	}
	if strings.HasPrefix(home, "~/") {
		home = filepath.Join(envValue(env, "HOME"), home[2:])
	}
	if !filepath.IsAbs(home) {
		return c, providerquota.Refuse("home_evidence_invalid")
	}
	home = filepath.Clean(home)
	c.Identity = &providerlimits.Identity{Key: providerlimits.IdentityKey(c.Runtime, home), Provider: c.Runtime, Home: home, HomeDisplay: home}
	return c, nil
}
func Store(root string, now time.Time) (*providerquota.Store, error) {
	return providerquota.NewStore(providerquota.Layout{Root: filepath.Join(root, "usage")}, providerquota.Options{Now: func() time.Time { return now }})
}
func Read(root string, env []string, now time.Time) ([]providerquota.QuotaRecord, error) {
	store, err := Store(root, now)
	if err != nil {
		return nil, err
	}
	records := []providerquota.QuotaRecord{}
	for _, runtime := range Runtimes {
		s, _ := System(runtime)
		c, err := ParseContext(s, env, now)
		if err != nil {
			return nil, err
		}
		r, err := store.Read(c)
		if errors.Is(err, providerquota.ErrAbsent) {
			continue
		}
		// Store supplies a sanitized unavailable record for unreadable/invalid data.
		if err != nil && r.Key == "" {
			return nil, err
		}
		records = append(records, r)
	}
	return records, nil
}
func Refresh(ctx context.Context, root, runtime string, env []string, now time.Time, ttl time.Duration) (providerquota.QuotaRecord, error) {
	if ttl < time.Second || ttl > 24*time.Hour || ttl%time.Second != 0 {
		return providerquota.QuotaRecord{}, providerquota.Refuse("ttl_invalid")
	}
	s, err := System(runtime)
	if err != nil {
		return providerquota.QuotaRecord{}, err
	}
	c, err := ParseContext(s, env, now)
	if err != nil {
		return providerquota.QuotaRecord{}, err
	}
	store, err := Store(root, now)
	if err != nil {
		return providerquota.QuotaRecord{}, err
	}
	key, err := c.Key()
	if err != nil {
		return providerquota.QuotaRecord{}, err
	}
	lock, err := store.Acquire(ctx, key)
	if err != nil {
		return providerquota.QuotaRecord{}, err
	}
	defer lock.Release()
	old, readErr := store.Read(c)
	if readErr == nil && !now.Before(old.RetrievedAt) && now.Sub(old.RetrievedAt) < time.Duration(old.TTLS)*time.Second {
		return old, nil
	}
	req := providerquota.Request{Context: c, Env: env, ForbiddenRoots: []string{filepath.Join(envValue(env, "HOME"), ".curator")}}
	var planErr error
	if runtime == "agy" {
		s, planErr = preflightAgy(ctx, req, root)
	}
	var dispatch providerquota.DispatchResult
	if planErr == nil {
		dispatch, planErr = providerquota.Dispatch(s, req)
	}
	var record providerquota.QuotaRecord
	var output []byte
	if planErr == nil && dispatch.Record != nil {
		record = *dispatch.Record
	} else if planErr == nil {
		output, planErr = Execute(ctx, *dispatch.Plan, root)
		if planErr == nil {
			record, planErr = dispatch.Plan.Parse(output, c)
		}
	}
	if planErr != nil {
		if record.Key == "" {
			record, _ = providerquota.Failure(c, "", providerquota.Reason(planErr))
		}
		if readErr == nil {
			record, err = providerquota.MergeRecord(old, record, providerquota.Failed, c)
			if err != nil {
				return providerquota.QuotaRecord{}, err
			}
		}
	}
	record.TTLS = int(ttl / time.Second)
	if err = store.Commit(lock, c, record); err != nil {
		return providerquota.QuotaRecord{}, err
	}
	return record, nil
}

var versionPattern = regexp.MustCompile(`\b([0-9]+\.[0-9]+\.[0-9]+)\b`)

func preflightAgy(ctx context.Context, req providerquota.Request, root string) (agentic.System, error) {
	binary, err := providerquota.ResolveBinary(req, providerquota.BinarySpec{Name: "agy"}, providerquota.NativeBinaryFS{})
	if err != nil {
		return nil, err
	}
	probe := func(arg string) ([]byte, error) {
		return Execute(ctx, providerquota.QuotaPlan{Binary: binary, Argv: []string{arg}, Env: req.Env, Timeout: 10 * time.Second, CwdPolicy: providerquota.ScratchCwd, Ready: true}, root)
	}
	version, err := probe("--version")
	if err != nil {
		return nil, err
	}
	match := versionPattern.FindSubmatch(version)
	if len(match) != 2 {
		return nil, providerquota.Refuse("version_unsupported")
	}
	help, err := probe("--help")
	if err != nil {
		return nil, err
	}
	for _, flag := range agy.RequiredQuotaFlags() {
		if !strings.Contains(string(help), flag) {
			return nil, providerquota.Refuse("flags_unsupported")
		}
	}
	return agy.NewWithRuntime(agy.Runtime{Executable: binary, Version: string(match[1])}), nil
}
