package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var (
	setTitle       string
	setDescription string
	setCategory    string
	setTags        string
)

var setCmd = &cobra.Command{
	Use:   "set <project>",
	Short: "Update metadata for an OSF project or component",
	Long: `Update writable metadata fields on an OSF project or component.

Only the flags you supply are sent in the PATCH request; unspecified
fields are left unchanged. At least one flag must be provided.

Available categories: analysis, communication, data, hypothesis,
instrumentation, methods and measures, procedure, project, software, other.

Examples:
  gosf set abc12 --description "Processed with pipeline v2.1"
  gosf set abc12 --title "Final Analysis" --category analysis
  gosf set abc12 --tags processed,qc-passed`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}

		token := config.LoadToken(flagToken)
		if token == "" {
			return fmt.Errorf("set requires authentication — run 'gosf auth login' or set OSF_TOKEN")
		}

		attrs := client.UpdateNodeAttrs{}
		anySet := false

		if cmd.Flags().Changed("title") {
			attrs.Title = &setTitle
			anySet = true
		}
		if cmd.Flags().Changed("description") {
			attrs.Description = &setDescription
			anySet = true
		}
		if cmd.Flags().Changed("category") {
			attrs.Category = &setCategory
			anySet = true
		}
		if cmd.Flags().Changed("tags") {
			parts := strings.Split(setTags, ",")
			tags := make([]string, 0, len(parts))
			for _, p := range parts {
				if t := strings.TrimSpace(p); t != "" {
					tags = append(tags, t)
				}
			}
			attrs.Tags = tags
			anySet = true
		}

		if !anySet {
			return fmt.Errorf("at least one flag required (--title, --description, --category, --tags)")
		}

		c := client.New(token)
		node, err := c.UpdateNode(cmd.Context(), target.NodeID, attrs)
		if err != nil {
			if apiErr, ok := err.(*client.APIError); ok {
				switch apiErr.StatusCode {
				case 404:
					return fmt.Errorf("project %q not found", target.NodeID)
				case 403:
					return fmt.Errorf("project %q is private or you lack write access", target.NodeID)
				}
			}
			return err
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, node)
		}
		if !flagQuiet {
			printNodeInfo(node)
		}
		return nil
	},
}

func init() {
	setCmd.Flags().StringVar(&setTitle, "title", "", "New project title")
	setCmd.Flags().StringVar(&setDescription, "description", "", "New project description")
	setCmd.Flags().StringVar(&setCategory, "category", "", "New project category")
	setCmd.Flags().StringVar(&setTags, "tags", "", "Comma-separated list of tags (replaces all existing tags)")
	rootCmd.AddCommand(setCmd)
}
