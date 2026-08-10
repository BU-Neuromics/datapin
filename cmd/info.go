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

var infoCmd = &cobra.Command{
	Use:          "info <project>",
	Short:        "Show metadata for an OSF project or component",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolver.ParseTarget(args[0])
		if err != nil {
			return err
		}

		c := client.New(config.LoadToken(flagToken))
		node, err := c.GetNode(cmd.Context(), target.NodeID)
		if err != nil {
			if apiErr, ok := err.(*client.APIError); ok {
				switch apiErr.StatusCode {
				case 404:
					return fmt.Errorf("project %q not found", target.NodeID)
				case 403:
					return fmt.Errorf("project %q is private — provide a token with access", target.NodeID)
				}
			}
			return err
		}

		if flagOutput == "json" {
			return output.PrintJSON(os.Stdout, node)
		}

		printNodeInfo(node)
		return nil
	},
}

func printNodeInfo(n *client.Node) {
	pub := "private"
	if n.Attributes.Public {
		pub = "public"
	}

	fmt.Printf("%s\n%s\n", n.Attributes.Title, strings.Repeat("=", len(n.Attributes.Title)))
	fmt.Printf("GUID:         %s\n", n.ID)
	fmt.Printf("Visibility:   %s\n", pub)
	if n.Attributes.Category != "" {
		fmt.Printf("Category:     %s\n", n.Attributes.Category)
	}
	fmt.Printf("Created:      %s\n", output.FormatDate(n.Attributes.DateCreated))
	fmt.Printf("Modified:     %s\n", output.FormatDate(n.Attributes.DateModified))
	if n.Attributes.Description != "" {
		fmt.Printf("Description:  %s\n", n.Attributes.Description)
	}
	fmt.Printf("URL:          https://osf.io/%s/\n", n.ID)
}

func init() {
	rootCmd.AddCommand(infoCmd)
}
