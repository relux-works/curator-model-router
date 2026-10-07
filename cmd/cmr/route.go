package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func routeFlags(args []string, allowed ...string) (map[string]string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 != len(args) {
				return nil, &Refusal{"cmr_invalid_arguments", "unexpected positional arguments"}
			}
			break
		}
		key, inlineValue, inline := strings.Cut(a, "=")
		a = key
		if _, ok := values[a]; ok {
			return nil, &Refusal{"cmr_invalid_arguments", "duplicate flag"}
		}
		if a == "--json" {
			value := "true"
			if inline {
				value = inlineValue
				if value != "true" && value != "false" {
					return nil, &Refusal{"cmr_invalid_arguments", "invalid boolean"}
				}
			}
			values[a] = value
			continue
		}
		found := false
		for _, v := range allowed {
			found = found || a == v
		}
		if !found || !inline && i+1 >= len(args) {
			return nil, &Refusal{"cmr_invalid_arguments", "invalid or missing flag value"}
		}
		if inline {
			values[a] = inlineValue
		} else {
			i++
			values[a] = args[i]
		}
	}
	return values, nil
}
func readInput(flags map[string]string, key string) ([]byte, error) {
	if flags[key] == "" {
		return nil, &Refusal{"cmr_invalid_arguments", "required flag: " + key}
	}
	raw, err := os.ReadFile(flags[key])
	if err != nil {
		return nil, &Refusal{"cmr_input_failed", "could not read supplied input"}
	}
	return raw, nil
}
func routeError(err error, args []string, out, stderr io.Writer) int {
	var refusal *Refusal
	var routed *routing.Error
	var canon *canonical.Error
	switch {
	case errors.As(err, &refusal):
	case errors.As(err, &routed):
		refusal = &Refusal{routed.Code, routed.Message}
	case errors.As(err, &canon):
		refusal = &Refusal{string(canon.Code), canon.Message}
	default:
		refusal = &Refusal{"cmr_invalid_input", "invalid input"}
	}
	return writeRefusal(refusal, hasJSON(args), out, stderr)
}
func routeOutput(value any, human string, args []string, out, stderr io.Writer) int {
	var err error
	if hasJSON(args) {
		err = json.NewEncoder(out).Encode(value)
	} else {
		_, err = io.WriteString(out, human)
	}
	if err != nil {
		return writeRefusal(&Refusal{"cmr_output_failed", "could not write routing output"}, hasJSON(args), out, stderr)
	}
	return 0
}
func bundleInput(flags map[string]string) (routing.DecisionBundle, error) {
	raw, err := readInput(flags, "--input")
	if err != nil {
		return routing.DecisionBundle{}, err
	}
	return routing.LoadBundle(raw)
}
func decisionInput(flags map[string]string) (routing.RoutingDecision, error) {
	raw, err := readInput(flags, "--decision")
	if err != nil {
		return routing.RoutingDecision{}, err
	}
	return routing.LoadDecision(raw)
}
func runRoute(args []string, out, stderr io.Writer) int {
	f, err := routeFlags(args, "--input")
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	b, err := bundleInput(f)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	r, err := routing.Route(b)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	human := "routing off\n"
	if r.Decision != nil {
		human = fmt.Sprintf("%s decision=%s\n", r.Decision.Outcome, r.Decision.DecisionID)
	}
	if r.EffectiveCandidate != nil {
		human += fmt.Sprintf("effective=%s\n", r.EffectiveCandidate.ID)
	}
	return routeOutput(r, human, args, out, stderr)
}
