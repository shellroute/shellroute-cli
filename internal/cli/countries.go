package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var countriesFormat string

var countriesCmd = &cobra.Command{
	Use:   "countries",
	Short: "List available countries and cities",
	RunE:  runCountries,
}

func init() {
	countriesCmd.Flags().StringVar(&countriesFormat, "format", "human", "Output format: human, json")
}

func runCountries(cmd *cobra.Command, args []string) error {
	if err := config.ValidateFormat(countriesFormat, "human", "json"); err != nil {
		display.Error("%s", err)
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	client := api.New(cfg.APIURL, cfg.APIKey)
	locs, err := client.GetLocations()
	if err != nil {
		display.Error("Cannot fetch locations. Check your connection.")
		return err
	}

	if countriesFormat == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(locs)
	}

	// Human-readable table
	headers := []string{"Country", "Code", "Residential", "Datacenter", "Cities"}
	var rows [][]string
	for _, loc := range locs.Locations {
		resi := "—"
		if loc.ResidentialCount > 0 {
			resi = fmt.Sprintf("✓ (%d)", loc.ResidentialCount)
		}
		dc := "—"
		if loc.DatacenterCount > 0 {
			dc = fmt.Sprintf("✓ (%d)", loc.DatacenterCount)
		}
		var cityNames []string
		for _, cd := range loc.Cities {
			cityNames = append(cityNames, cd.Name)
		}
		cities := strings.Join(cityNames, ", ")
		if len(cities) > 40 {
			cities = cities[:37] + "..."
		}
		rows = append(rows, []string{loc.CountryName, loc.Country, resi, dc, cities})
	}

	display.Table(headers, rows)
	display.Blank()
	display.Dim("%d countries available.", len(locs.Locations))
	return nil
}
