package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var citiesFormat string

var citiesCmd = &cobra.Command{
	Use:   "cities <country>",
	Short: "List available cities in a country",
	Long:  "Show available cities with residential and datacenter IP counts.",
	Example: `  shellroute cities US
  shellroute cities DE --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runCities,
}

func init() {
	citiesCmd.Flags().StringVar(&citiesFormat, "format", "human", "Output format: human, json")
}

func runCities(cmd *cobra.Command, args []string) error {
	if err := config.ValidateFormat(citiesFormat, "human", "json"); err != nil {
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

	code := config.NormalizeCountry(args[0])
	client := api.New(cfg.APIURL, cfg.APIKey)
	locs, err := client.GetLocations()
	if err != nil {
		display.Error("Cannot fetch locations. Check your connection.")
		return err
	}

	for _, loc := range locs.Locations {
		if loc.Country != code {
			continue
		}

		if citiesFormat == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(loc.Cities)
		}

		if len(loc.Cities) == 0 {
			display.Dim("No cities listed for %s (%s).", loc.CountryName, code)
			return nil
		}

		headers := []string{"City", "Residential", "Datacenter"}
		var rows [][]string
		for _, city := range loc.Cities {
			rows = append(rows, []string{
				city.Name,
				fmt.Sprintf("%d", city.ResidentialCount),
				fmt.Sprintf("%d", city.DatacenterCount),
			})
		}

		display.Table(headers, rows)
		display.Blank()
		display.Dim("%s (%s) — %d cities, %d res / %d dc total.",
			loc.CountryName, code, len(loc.Cities), loc.ResidentialCount, loc.DatacenterCount)
		return nil
	}

	display.Error("Unknown country code: %s. Run `shellroute countries` to see available.", code)
	return fmt.Errorf("unknown country: %s", code)
}
