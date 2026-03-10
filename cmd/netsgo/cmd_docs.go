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
	Short: "生成命令行文档",
	Long: `自动生成 NetsGo 命令行文档。

支持 Markdown 和 Man Page 两种格式，默认输出到 ./docs/cli/ 目录。`,
	Example: `  # 生成 Markdown 文档
  netsgo docs

  # 生成 Markdown 文档到指定目录
  netsgo docs --output ./my-docs

  # 生成 Man Page 格式
  netsgo docs --format man`,
	Run: func(cmd *cobra.Command, args []string) {
		outputDir, _ := cmd.Flags().GetString("output")
		format, _ := cmd.Flags().GetString("format")

		// 确保输出目录存在
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			log.Fatalf("❌ 创建输出目录失败: %v", err)
		}

		absPath, _ := filepath.Abs(outputDir)

		switch format {
		case "markdown", "md":
			if err := doc.GenMarkdownTree(rootCmd, outputDir); err != nil {
				log.Fatalf("❌ 生成 Markdown 文档失败: %v", err)
			}
			fmt.Printf("✅ Markdown 文档已生成到: %s\n", absPath)
		case "man":
			header := &doc.GenManHeader{
				Title:   "NETSGO",
				Section: "1",
				Source:  "NetsGo " + version,
			}
			if err := doc.GenManTree(rootCmd, header, outputDir); err != nil {
				log.Fatalf("❌ 生成 Man Page 失败: %v", err)
			}
			fmt.Printf("✅ Man Page 已生成到: %s\n", absPath)
		default:
			log.Fatalf("❌ 不支持的格式: %s (支持 markdown, man)", format)
		}
	},
}

func init() {
	docsCmd.Flags().StringP("output", "o", "./docs/cli", "文档输出目录")
	docsCmd.Flags().StringP("format", "f", "markdown", "文档格式 (markdown / man)")

	rootCmd.AddCommand(docsCmd)
}
