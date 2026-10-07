package routing

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

const (
	ContractMissingField        = "contract_missing_field"
	ContractConfigOrderMismatch = "contract_config_order_mismatch"
	ContractBasePositionInvalid = "contract_base_position_invalid"
	ContractEmptyField          = "contract_empty_field"
	ContractUnknownField        = "contract_unknown_field"
	ContractFieldForbidden      = "contract_field_forbidden"
	ContractInvalidDigest       = "contract_invalid_digest"
	ContractFreshnessMismatch   = "contract_freshness_mismatch"
	ContractDecisionIDMismatch  = "contract_decision_id_mismatch"
	EstimatorSnapshotMismatch   = "estimator_snapshot_mismatch"
	RoutingSelectionMismatch    = "routing_selection_mismatch"
)

// NewEstimatorSnapshotMismatchError constructs the typed refusal when evidence
// supplied by reference differs from the estimator's immutable in-memory data.
// It uses the shared routing Error type, so callers can inspect Code with errors.As.
func NewEstimatorSnapshotMismatchError() *Error {
	return &Error{Code: EstimatorSnapshotMismatch, Message: "evidence snapshot digest differs from estimator evidence"}
}

// validateFields enforces JSON presence recursively from the declared tags.
// Optional values are omitted when unknown; present pointers are checked as values.
// WindowFilter owns a custom wire shape and is validated by RoutingPolicy instead.
func validateFields(value any) error { return checkFields(reflect.ValueOf(value), "$", false) }
func checkFields(v reflect.Value, path string, optional bool) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return checkFields(v.Elem(), path, false)
	}
	if v.Type() == reflect.TypeFor[WindowFilter]() {
		return nil
	}
	if v.Type() == reflect.TypeFor[UsageFact]() {
		fact := v.Interface().(UsageFact)
		if fact.State == StateAbsent {
			if fact.RecordDigest != nil {
				return fail(ContractFieldForbidden, path+".record_digest is forbidden for absent facts")
			}
			if len(fact.Windows) != 0 {
				return fail(ContractFieldForbidden, path+".windows must be empty for absent facts")
			}
		} else if fact.RecordDigest == nil {
			return fail(ContractMissingField, path+".record_digest is required for non-absent facts")
		}
	}
	switch v.Kind() {
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				continue
			}
			if err := checkFields(v.Field(i), path+"."+tag[0], slices.Contains(tag[1:], "omitempty")); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if v.IsNil() && !optional {
			return fail(ContractMissingField, path+" is required; use [] for known-empty")
		}
		for i := 0; i < v.Len(); i++ {
			if err := checkFields(v.Index(i), fmt.Sprintf("%s[%d]", path, i), false); err != nil {
				return err
			}
		}
	case reflect.Map:
		// Sort keys so multiple invalid facets/features have a stable first refusal.
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, key := range keys {
			if err := checkFields(key, path+" map key", false); err != nil {
				return err
			}
			if err := checkFields(v.MapIndex(key), fmt.Sprintf("%s[%q]", path, key.String()), false); err != nil {
				return err
			}
		}
	case reflect.String:
		text := v.String()
		if optional && text == "" {
			return nil
		}
		if text == "" {
			return fail(ContractEmptyField, path+` is empty; use "unknown" for an unknown required string`)
		}
		// Profile fingerprints may be unknown; identity and evidence digests cannot.
		if text == Unknown && (strings.HasSuffix(path, ".engine_profile.weight_digest") || strings.HasSuffix(path, ".context_profile.lock_sha256")) {
			return nil
		}
		if (strings.HasSuffix(path, "digest") || strings.HasSuffix(path, ".lock_sha256") || strings.HasSuffix(path, ".decision_id")) && !validDigest(text) {
			return fail(ContractInvalidDigest, path+" must match sha256:<64 lowercase hex>")
		}
	}
	return nil
}
func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	for _, r := range value[7:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (v TaskEnvelope) Validate() error       { return validateFields(v) }
func (v ExecutionCandidate) Validate() error { return validateCandidate(v) }
func (v Requirements) Validate() error       { return validateFields(v) }
func (v EvidenceSnapshotRef) Validate() error {
	if err := validateFields(v); err != nil {
		return err
	}
	return checkAddresses(v.RecordIDs, false)
}
func (v Estimate) Validate() error {
	if err := validateFields(v); err != nil {
		return err
	}
	if (v.Fitness != nil && (*v.Fitness < 0 || *v.Fitness > 10000)) || (v.Coverage != nil && (*v.Coverage < 0 || *v.Coverage > 10000)) {
		return fail("routing_invalid_estimate", "fitness/coverage must be between 0 and 10000 bp")
	}
	return checkAddresses(v.ContributingRecordIDs, false)
}
