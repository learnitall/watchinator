package cmd

import (
	"github.com/spf13/cobra"
)

var (
	validateConfigCmd = &cobra.Command{
		Use:   "validate-config",
		Short: "Validate config file",
		Run: func(cmd *cobra.Command, args []string) {
			initConfigOrDie()
			validateConfigOrDie()
		},
	}
)

func init() {
	rootCmd.AddCommand(validateConfigCmd)
}
