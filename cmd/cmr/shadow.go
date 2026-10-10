package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

// Nullable dimensions describe only explicit caller flags, never guessed defaults.
type shadowPair struct {
	Runtime *string `json:"runtime"`
	Model   *string `json:"model"`
	Effort  *string `json:"effort"`
}

type shadowPick struct {
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Code    string `json:"code,omitempty"`
}

type shadowObservation struct {
	SchemaVersion     string     `json:"schema_version"`
	Time              time.Time  `json:"time"`
	DecisionID        *string    `json:"decision_id"`
	Role              string     `json:"role"`
	OriginalTaskClass string     `json:"original_task_class"`
	TaskClass         string     `json:"task_class"`
	Difficulty        string     `json:"difficulty"`
	Sensitivity       string     `json:"sensitivity"`
	Budget            string     `json:"budget"`
	CMRPick           shadowPick `json:"cmr_pick"`
	CallerPair        shadowPair `json:"caller_pair"`
	Agreement         string     `json:"agreement"`
	TaskBoardExitCode int        `json:"task_board_exit_code"`
	CMRError          *Refusal   `json:"cmr_error,omitempty"`
}

var shadowAgreementKinds = []string{"exact", "same_model_other_effort", "same_runtime_other_model", "different_runtime", "unknown_caller_default", "no_cmr_pick"}

func shadowAgreement(caller shadowPair, pick shadowPick) string {
	if pick.Code != "" || pick.Runtime == "" {
		return "no_cmr_pick"
	}
	if caller.Runtime == nil || caller.Model == nil || caller.Effort == nil {
		return "unknown_caller_default"
	}
	if *caller.Runtime != pick.Runtime {
		return "different_runtime"
	}
	if canonicalModel(*caller.Model) != canonicalModel(pick.Model) {
		return "same_runtime_other_model"
	}
	if *caller.Effort != pick.Effort {
		return "same_model_other_effort"
	}
	return "exact"
}

func newShadowObservation(o recommendOptions, parsed spawnArguments, policy recommend.Policy, record recommend.DecisionRecord, recErr error, status int, at time.Time) shadowObservation {
	role := o.role
	if value, ok := parsed.locks["role"]; ok {
		role = value
	}
	task := recommend.TaskProfile{Role: role, TaskClass: recommend.MapTaskClass(o.taskClass, role), Difficulty: o.difficulty, Sensitivity: "normal"}
	if o.delicate {
		task.Sensitivity = "delicate"
	}
	// Normalize defaults even when a later advisory operation fails. Invalid
	// task values remain visible rather than being silently replaced.
	if normalized, err := task.Normalize(); err == nil {
		task = normalized
	}
	budget := policy.BudgetMode
	if budget == "" {
		budget = "unknown"
	}
	if o.budget != "" {
		budget = o.budget
	}
	if record.SchemaVersion != "" {
		// A frozen decision states the effective budget, including the
		// legacy empty wire value that means balanced.
		task = record.Inputs.Task
		budget = record.Inputs.Policy.EffectiveBudgetMode()
	}
	if budget == "" {
		budget = "unknown"
	}
	observation := shadowObservation{SchemaVersion: "shadow-observation-v1", Time: at.UTC(), OriginalTaskClass: o.taskClass, Role: task.Role, TaskClass: task.TaskClass, Difficulty: task.Difficulty, Sensitivity: task.Sensitivity, Budget: budget, TaskBoardExitCode: status}
	if record.DecisionID != "" && recErr == nil {
		observation.DecisionID = &record.DecisionID
	}
	for _, dim := range []struct {
		name   string
		target **string
	}{{"agent", &observation.CallerPair.Runtime}, {"model", &observation.CallerPair.Model}, {"reasoning-effort", &observation.CallerPair.Effort}} {
		if value, ok := parsed.locks[dim.name]; ok {
			v := value
			*dim.target = &v
		}
	}
	switch {
	case recErr != nil:
		observation.CMRError = sanitizedShadowError(recErr, o.taskClass)
		observation.CMRPick.Code = observation.CMRError.Code
	case record.Recommendation.Refusal != nil:
		observation.CMRPick.Code = record.Recommendation.Refusal.Code
	case record.Recommendation.Selected != nil:
		c := record.Recommendation.Selected
		observation.CMRPick = shadowPick{Runtime: c.Runtime, Model: c.Model, Effort: c.Effort}
	default:
		observation.CMRPick.Code = "no_qualified_candidate"
	}
	observation.Agreement = shadowAgreement(observation.CallerPair, observation.CMRPick)
	return observation
}

// Keep the typed refusal code and a fixed public message. Never copy parser,
// filesystem, or provider diagnostics into a durable observation.
func sanitizedShadowError(err error, originalClass string) *Refusal {
	var encoded bytes.Buffer
	commandError(err, true, &encoded, io.Discard)
	var wire struct {
		Error Refusal `json:"error"`
	}
	_ = json.Unmarshal(encoded.Bytes(), &wire)
	code := wire.Error.Code
	message := map[string]string{
		"preflight_timeout":         "spawn-preflight timed out",
		"preflight_failed":          "spawn-preflight failed",
		"advisory_timeout":          "advisory recommendation timed out",
		"invalid_admission":         "invalid spawn-preflight or candidate admission",
		"invalid_policy":            "invalid routing policy",
		"invalid_catalog":           "invalid routing catalog",
		"invalid_catalog_overlay":   "invalid catalog overlay",
		"invalid_local_capability":  "invalid local capability document",
		"cmr_decision_write_failed": "cannot store decision",
		"cmr_invalid_arguments":     "invalid or conflicting advisory arguments",
		"cmr_host_unavailable":      "cannot determine host",
		"cmr_usage_failed":          "provider usage operation failed",
		"cmr_usage_locked":          "provider usage refresh is locked; retry",
	}[code]
	if code == "invalid_task" {
		// Normalize supplied facets independently to expose only known task errors.
		if recommend.MapTaskClass(originalClass) == originalClass && strings.HasPrefix(wire.Error.Message, "unknown class ") {
			if len(originalClass) > 80 {
				originalClass = originalClass[:80] + "..."
			}
			message = fmt.Sprintf("unknown class %q", originalClass)
		} else {
			message = "invalid task profile"
			for _, fixed := range []string{"role is required", "unknown difficulty", "unknown sensitivity", "unknown pipeline", "original task class does not match mapped class"} {
				if wire.Error.Message == fixed {
					message = fixed
					break
				}
			}
		}
	}
	if message == "" {
		message = "advisory recommendation refused"
	}
	return &Refusal{Code: code, Message: message}
}

func shadowObservationPath(root string) string {
	return filepath.Join(root, "shadow", "observations.jsonl")
}

func appendShadowObservation(root string, observation shadowObservation) error {
	data, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	path := shadowObservationPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	// One append write per complete line avoids a read/modify/write race between
	// independent shadow processes. Never truncate or rewrite earlier observations.
	data = append(data, '\n')
	n, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return closeErr
}

type shadowTotals struct {
	Observations          int `json:"observations"`
	Comparisons           int `json:"comparisons"`
	Agreements            int `json:"agreements"`
	Divergences           int `json:"divergences"`
	UnknownCallerDefaults int `json:"unknown_caller_defaults"`
	Refusals              int `json:"refusals"`
	FailOpenLaunches      int `json:"fail_open_launches"`
	NonzeroTaskBoardExits int `json:"nonzero_task_board_exits"`
	// TruncatedTail is set when the log ends in an unterminated fragment (an
	// interrupted append). The fragment is excluded; complete lines still count.
	TruncatedTail bool `json:"truncated_tail"`
}

type shadowDivergence struct {
	Role       string     `json:"role"`
	TaskClass  string     `json:"task_class"`
	Difficulty string     `json:"difficulty"`
	CallerPair shadowPair `json:"caller_pair"`
	CMRPick    shadowPick `json:"cmr_pick"`
	Count      int        `json:"count"`
}

type shadowRefusalCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type shadowReport struct {
	SchemaVersion         string               `json:"schema_version"`
	Since                 *time.Time           `json:"since"`
	Totals                shadowTotals         `json:"totals"`
	AgreementDistribution map[string]int       `json:"agreement_distribution"`
	TopDivergences        []shadowDivergence   `json:"top_divergences"`
	Refusals              []shadowRefusalCount `json:"refusals"`
	FailOpenByCode        []shadowRefusalCount `json:"fail_open_by_code"`
	FailOpenLaunches      []shadowObservation  `json:"fail_open_launches"`
}

// Check the envelope as well as JSON syntax so damaged observations cannot
// become successful comparisons merely through zero-valued decoded fields.
func decodeShadowObservation(data []byte) (shadowObservation, bool) {
	var observation shadowObservation
	if json.Unmarshal(data, &observation) != nil || observation.SchemaVersion != "shadow-observation-v1" || observation.Time.IsZero() {
		return observation, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return observation, false
	}
	for _, key := range []string{"time", "decision_id", "role", "task_class", "difficulty", "sensitivity", "budget", "cmr_pick", "caller_pair", "agreement", "task_board_exit_code"} {
		value, exists := fields[key]
		if !exists || (key != "decision_id" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return observation, false
		}
	}
	var caller map[string]json.RawMessage
	if json.Unmarshal(fields["caller_pair"], &caller) != nil {
		return observation, false
	}
	for _, key := range []string{"runtime", "model", "effort"} {
		if _, exists := caller[key]; !exists {
			return observation, false
		}
	}
	pick := observation.CMRPick
	if pick.Code == "" && (pick.Runtime == "" || pick.Model == "" || pick.Effort == "") {
		return observation, false
	}
	if pick.Code != "" && (pick.Runtime != "" || pick.Model != "" || pick.Effort != "") {
		return observation, false
	}
	if observation.CMRError != nil && observation.CMRError.Code == "" {
		return observation, false
	}
	return observation, true
}

func readShadowReport(root string, since *time.Time) (shadowReport, error) {
	report := shadowReport{SchemaVersion: "shadow-report-v1", Since: since, AgreementDistribution: map[string]int{}, TopDivergences: []shadowDivergence{}, Refusals: []shadowRefusalCount{}, FailOpenByCode: []shadowRefusalCount{}, FailOpenLaunches: []shadowObservation{}}
	for _, kind := range shadowAgreementKinds {
		report.AgreementDistribution[kind] = 0
	}
	f, err := os.Open(shadowObservationPath(root))
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, &Refusal{Code: "cmr_shadow_read_failed", Message: "cannot read shadow observations"}
	}
	defer f.Close()
	groups := map[string]shadowDivergence{}
	refusals := map[string]int{}
	failOpen := map[string]int{}
	reader := bufio.NewReaderSize(f, 64*1024)
	truncated := false
	line := 0
	for {
		// ReadBytes has no line-length limit, so even an oversized partial
		// tail is recognised as the final fragment instead of a read failure.
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return report, &Refusal{Code: "cmr_shadow_read_failed", Message: "cannot read complete shadow observations"}
		}
		if len(raw) == 0 {
			break
		}
		terminated := raw[len(raw)-1] == '\n'
		raw = bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte("\n")), []byte("\r"))
		line++
		o, valid := decodeShadowObservation(raw)
		if !valid && !terminated {
			// Only the final record can be unterminated. A complete record that
			// lost just its newline still counts; a partial one is skipped.
			truncated = true
			break
		}
		if !valid {
			return report, &Refusal{Code: "cmr_shadow_invalid_observation", Message: fmt.Sprintf("invalid shadow observation at line %d", line)}
		}
		if since != nil && o.Time.Before(*since) {
			continue
		}
		// Reclassify from the actual fields so a failed advisory path cannot ever
		// contribute to agreement, even if a stored label was incorrectly assigned.
		o.Agreement = shadowAgreement(o.CallerPair, o.CMRPick)
		if o.CMRError != nil {
			o.Agreement = "no_cmr_pick"
		}
		report.Totals.Observations++
		report.AgreementDistribution[o.Agreement]++
		if o.TaskBoardExitCode != 0 {
			report.Totals.NonzeroTaskBoardExits++
		}
		switch o.Agreement {
		case "exact":
			report.Totals.Comparisons++
			report.Totals.Agreements++
		case "same_model_other_effort", "same_runtime_other_model", "different_runtime":
			report.Totals.Comparisons++
			report.Totals.Divergences++
			group := shadowDivergence{Role: o.Role, TaskClass: o.TaskClass, Difficulty: o.Difficulty, CallerPair: o.CallerPair, CMRPick: o.CMRPick}
			key, _ := json.Marshal(group)
			existing := groups[string(key)]
			group.Count = existing.Count + 1
			groups[string(key)] = group
		case "unknown_caller_default":
			report.Totals.UnknownCallerDefaults++
		}
		if o.CMRError != nil {
			report.Totals.FailOpenLaunches++
			failOpen[o.CMRError.Code]++
			report.FailOpenLaunches = append(report.FailOpenLaunches, o)
		} else if o.CMRPick.Code != "" {
			report.Totals.Refusals++
			refusals[o.CMRPick.Code]++
		}
	}
	report.Totals.TruncatedTail = truncated
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if groups[keys[i]].Count != groups[keys[j]].Count {
			return groups[keys[i]].Count > groups[keys[j]].Count
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		report.TopDivergences = append(report.TopDivergences, groups[key])
	}
	for code, count := range refusals {
		report.Refusals = append(report.Refusals, shadowRefusalCount{Code: code, Count: count})
	}
	sort.Slice(report.Refusals, func(i, j int) bool {
		if report.Refusals[i].Count != report.Refusals[j].Count {
			return report.Refusals[i].Count > report.Refusals[j].Count
		}
		return report.Refusals[i].Code < report.Refusals[j].Code
	})
	for code, count := range failOpen {
		report.FailOpenByCode = append(report.FailOpenByCode, shadowRefusalCount{Code: code, Count: count})
	}
	sort.Slice(report.FailOpenByCode, func(i, j int) bool {
		if report.FailOpenByCode[i].Count != report.FailOpenByCode[j].Count {
			return report.FailOpenByCode[i].Count > report.FailOpenByCode[j].Count
		}
		return report.FailOpenByCode[i].Code < report.FailOpenByCode[j].Code
	})
	return report, nil
}

func renderShadowReport(out io.Writer, report shadowReport) error {
	// Buffer all human output so every output failure follows the same CLI path.
	var b bytes.Buffer
	t := report.Totals
	fmt.Fprintf(&b, "Shadow: %d observations; %d comparisons; %d exact; %d divergences\n", t.Observations, t.Comparisons, t.Agreements, t.Divergences)
	fmt.Fprintf(&b, "Unknown caller defaults: %d; refusals: %d; FAIL-OPEN launches: %d; nonzero task-board exits: %d\n", t.UnknownCallerDefaults, t.Refusals, t.FailOpenLaunches, t.NonzeroTaskBoardExits)
	if t.TruncatedTail {
		fmt.Fprintln(&b, "WARNING: the log ends in an incomplete observation (interrupted append); it is excluded")
	}
	fmt.Fprintln(&b, "Agreement distribution:")
	for _, kind := range shadowAgreementKinds {
		fmt.Fprintf(&b, "  %s: %d\n", kind, report.AgreementDistribution[kind])
	}
	fmt.Fprintln(&b, "Top divergences (by count):")
	for _, d := range report.TopDivergences {
		caller, _ := json.Marshal(d.CallerPair)
		pick, _ := json.Marshal(d.CMRPick)
		fmt.Fprintf(&b, "  %d %s/%s/%s %s -> %s\n", d.Count, d.Role, d.TaskClass, d.Difficulty, caller, pick)
	}
	fmt.Fprintln(&b, "Refusals:")
	for _, r := range report.Refusals {
		fmt.Fprintf(&b, "  %s: %d\n", r.Code, r.Count)
	}
	fmt.Fprintln(&b, "FAIL-OPEN launches by code:")
	for _, r := range report.FailOpenByCode {
		fmt.Fprintf(&b, "  %s: %d\n", r.Code, r.Count)
	}
	fmt.Fprintln(&b, "FAIL-OPEN launches (advisory errors; excluded from comparisons):")
	for _, o := range report.FailOpenLaunches {
		id := "null"
		if o.DecisionID != nil {
			id = *o.DecisionID
		}
		caller, _ := json.Marshal(o.CallerPair)
		// JSON quoting prevents caller task facets or codes from injecting log lines.
		fmt.Fprintf(&b, "  %s decision=%s role=%q task=%q difficulty=%q caller=%s error=%q task_board_exit_code=%d\n", o.Time.UTC().Format(time.RFC3339Nano), id, o.Role, o.TaskClass, o.Difficulty, caller, o.CMRError.Code, o.TaskBoardExitCode)
	}
	_, err := out.Write(b.Bytes())
	return err
}

func runShadow(args []string, stdout, stderr io.Writer) int {
	invalid := &Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr shadow report [--since RFC3339] [--json]"}
	if len(args) == 0 || args[0] != "report" {
		return writeRefusal(invalid, hasJSON(args), stdout, stderr)
	}
	f := flag.NewFlagSet("shadow report", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	sinceFlag := f.String("since", "", "")
	asJSON := f.Bool("json", false, "")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return writeRefusal(invalid, hasJSON(args), stdout, stderr)
	}
	var since *time.Time
	sincePresent := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "since" {
			sincePresent = true
		}
	})
	if sincePresent {
		parsed, err := time.Parse(time.RFC3339, *sinceFlag)
		if err != nil {
			return writeRefusal(invalid, *asJSON, stdout, stderr)
		}
		parsed = parsed.UTC()
		since = &parsed
	}
	report, err := readShadowReport(cmrio.StateRoot(), since)
	if err != nil {
		return commandError(err, *asJSON, stdout, stderr)
	}
	if *asJSON {
		err = json.NewEncoder(stdout).Encode(report)
	} else {
		err = renderShadowReport(stdout, report)
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write shadow report"}, *asJSON, stdout, stderr)
	}
	return 0
}
