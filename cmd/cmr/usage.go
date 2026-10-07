package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/internal/quota"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

func runUsage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" && args[0] != "refresh" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr usage show|refresh [--json]"}, hasJSON(args), stdout, stderr)
	}
	f := flag.NewFlagSet("usage", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	asJSON := f.Bool("json", false, "")
	var runtime string
	ttl := 10 * time.Minute
	if args[0] == "refresh" {
		f.StringVar(&runtime, "runtime", "", "")
		f.DurationVar(&ttl, "ttl", ttl, "")
	}
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "invalid usage flags"}, hasJSON(args), stdout, stderr)
	}
	root := cmrio.StateRoot()
	env := os.Environ()
	records := []providerquota.QuotaRecord{}
	if args[0] == "refresh" {
		if ttl < time.Second || ttl > 24*time.Hour || ttl%time.Second != 0 {
			return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "ttl must be whole seconds between 1s and 24h"}, *asJSON, stdout, stderr)
		}
		runtimes := quota.Runtimes
		if runtime != "" {
			runtimes = []string{runtime}
		}
		for _, r := range runtimes {
			record, err := quota.Refresh(context.Background(), root, r, env, time.Now().UTC(), ttl)
			if err != nil {
				return commandError(err, *asJSON, stdout, stderr)
			}
			records = append(records, record)
		}
	} else {
		var err error
		records, err = quota.Read(root, env, time.Now().UTC())
		if err != nil {
			return commandError(err, *asJSON, stdout, stderr)
		}
	}
	projections := []providerquota.Projection{}
	for _, r := range records {
		projections = append(projections, r.RouterProjection())
	}
	var err error
	if *asJSON {
		err = json.NewEncoder(stdout).Encode(struct {
			Records  []providerquota.Projection `json:"records"`
			Coverage providerquota.Coverage     `json:"coverage"`
		}{projections, providerquota.Summarize(records)})
	} else {
		for _, r := range projections {
			if _, err = fmt.Fprintf(stdout, "%s key=%s state=%s ttl=%ds windows=%d failures=%d\n", r.Runtime, r.Identity, r.State, r.TTLS, len(r.Windows), len(r.Failures)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write usage"}, *asJSON, stdout, stderr)
	}
	return 0
}
