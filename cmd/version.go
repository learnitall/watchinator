package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	commit string = "n/a"
	tag    string = "n/a"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "print version and exit",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("watchinator: tag=%s, commit=%s\n", tag, commit)
	},
}
