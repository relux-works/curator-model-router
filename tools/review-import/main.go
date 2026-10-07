// review-import reads only explicit local files and writes a reproducible report.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/relux-works/curator-model-router/internal/reviewbench"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func main() {
	csvPath := flag.String("csv", "", "local pinned runs.csv (required)")
	catalogPath := flag.String("catalog", "catalog/catalog.json", "local input catalog")
	output := flag.String("catalog-out", "", "optional output catalog; report goes to stdout")
	format := flag.String("format", "json", "report format: json or markdown")
	flag.Parse()
	if *csvPath == "" || flag.NArg() != 0 || (*format != "json" && *format != "markdown") {
		flag.Usage()
		os.Exit(2)
	}
	if err := execute(*csvPath, *catalogPath, *output, *format); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func execute(csvPath, catalogPath, output, format string) error {
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		return err
	}
	catalogRaw, err := os.ReadFile(catalogPath)
	if err != nil {
		return err
	}
	c, err := recommend.LoadCatalog(catalogRaw)
	if err != nil {
		return err
	}
	report, err := reviewbench.Import(raw, c)
	if err != nil {
		return err
	}
	if report.CSVSHA256 != reviewbench.PinnedCSVSHA256 {
		return fmt.Errorf("CSV digest differs from the pinned source; update the source pin before importing another snapshot")
	}
	if output != "" {
		c = reviewbench.Apply(c, report)
		if err = c.Validate(); err != nil {
			return err
		}
		// Keep the human-facing catalog's runtime/model/effort display order.
		// The selector independently normalizes canonical configuration keys.
		slices.SortFunc(c.Rows, func(a, b recommend.CatalogRow) int {
			if n := strings.Compare(a.Runtime, b.Runtime); n != 0 {
				return n
			}
			if n := strings.Compare(a.Model, b.Model); n != 0 {
				return n
			}
			return strings.Compare(a.Effort, b.Effort)
		})
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(output, append(b, '\n'), 0644); err != nil {
			return err
		}
	}
	if format == "markdown" {
		_, err := fmt.Fprint(os.Stdout, report.Table())
		for _, group := range report.Unmapped {
			fmt.Fprintf(os.Stderr, "unmapped: %s/%s/%s (%d runs): %s\n", group.Harness, group.Model, group.Effort, len(group.Runs), group.Reason)
		}
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
