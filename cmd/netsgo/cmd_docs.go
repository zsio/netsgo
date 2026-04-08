package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate CLI documentation",
	Long: `Auto-generate NetsGo CLI documentation.

Supports Markdown and Man Page formats. Defaults to ./docs/cli/ output directory.`,
	Example: `  # Generate Markdown documentation
  netsgo docs

  # Generate to a specific directory
  netsgo docs --output ./my-docs

  # Generate Man Page format
  netsgo docs --format man`,
	Run: func(cmd *cobra.Command, args []string) {
		outputDir, _ := cmd.Flags().GetString("output")
		format, _ := cmd.Flags().GetString("format")

		if err := os.MkdirAll(outputDir, 0755); err != nil {
			log.Fatalf("❌ Failed to create output directory: %v", err)
		}

		absPath, _ := filepath.Abs(outputDir)

		switch format {
		case "markdown", "md":
			if err := doc.GenMarkdownTree(rootCmd, outputDir); err != nil {
				log.Fatalf("❌ Failed to generate Markdown docs: %v", err)
			}
			fmt.Printf("✅ Markdown docs generated to: %s\n", absPath)
		case "man":
			header := &doc.GenManHeader{
				Title:   "NETSGO",
				Section: "1",
				Source:  "NetsGo " + version,
			}
			if err := doc.GenManTree(rootCmd, header, outputDir); err != nil {
				log.Fatalf("❌ Failed to generate Man Page: %v", err)
			}
			fmt.Printf("✅ Man Page generated to: %s\n", absPath)
		default:
			log.Fatalf("❌ Unsupported format: %s (supported: markdown, man)", format)
		}
	},
}

func init() {
	docsCmd.Flags().StringP("output", "o", "./docs/cli", "Documentation output directory")
	docsCmd.Flags().StringP("format", "f", "markdown", "Documentation format (markdown / man)")

	rootCmd.AddCommand(docsCmd)
}
