package quota

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/curator-model-router/pkg/routing"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

func Project(records []providerquota.QuotaRecord, catalog recommend.Catalog, asOf time.Time) recommend.UsageSnapshot {
	out := recommend.UsageSnapshot{AsOf: asOf.Unix(), UsageKeys: map[string]string{}}
	for _, r := range records {
		// The digest binds the sanitized public record, never home display/flags.
		projection := r.RouterProjection()
		b, _ := json.Marshal(projection)
		sum := sha256String(b)
		f := routing.UsageFact{Key: r.Key, Runtime: r.Runtime, State: routing.State(r.State), Windows: []routing.UsageWindow{}, RecordDigest: &sum, FailureCount: int64(len(r.Failures))}
		if r.Credits != nil {
			balance, unlimited := r.Credits.Balance, r.Credits.Unlimited
			f.Credits = &routing.Credits{Balance: &balance, Unit: r.Credits.Unit, Unlimited: &unlimited}
		}
		for _, w := range r.Windows {
			id, _ := json.Marshal([]string{w.ID, w.Kind, w.Scope})
			x := routing.UsageWindow{ID: string(id), Scope: w.Scope, Minutes: int64(w.Minutes)}
			// Providerquota leaves account-wide windows unscoped. Routing uses
			// the explicit "all" scope for the same applicability.
			if x.Scope == "" {
				x.Scope = "all"
			}
			if w.UsedPercent != nil {
				bp, err := routing.PercentToBP(strconv.FormatFloat(*w.UsedPercent, 'f', -1, 64))
				if err == nil {
					x.UsedBP = &bp
				}
			}
			if w.ObservedAt != nil {
				t := w.ObservedAt.Unix()
				x.ObservedAt = &t
			}
			if w.ResetsAt != nil {
				t := w.ResetsAt.Unix()
				x.ResetsAt = &t
			}
			x.Freshness = routing.ClassifyFreshness(x, out.AsOf, int64(r.TTLS))
			f.Windows = append(f.Windows, x)
			// A scoped window applies only to an exact catalog model. Unknown group
			// names remain unmapped and neutral instead of covering every model.
			if w.Scope != "" {
				found := false
				for _, row := range catalog.Rows {
					if row.Runtime == r.Runtime && row.Model == w.Scope {
						found = true
						break
					}
				}
				if found {
					exists := false
					for _, entry := range out.ScopeMap.Entries {
						if entry.Runtime == r.Runtime && entry.Scope == w.Scope {
							exists = true
						}
					}
					if !exists {
						out.ScopeMap.Entries = append(out.ScopeMap.Entries, routing.ScopeMapEntry{Runtime: r.Runtime, Scope: w.Scope, ModelIDs: []string{w.Scope}})
					}
				}
			}
		}
		out.Facts = append(out.Facts, f)
		for _, row := range catalog.Rows {
			if row.Runtime == r.Runtime {
				out.UsageKeys[row.Key()] = r.Key
			}
		}
	}
	return out
}

func sha256String(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
