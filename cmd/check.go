package cmd

import (
	"fmt"
	"os"

	"github.com/learnitall/watchinator/pkg"
	"github.com/spf13/cobra"
)

var (
	checkCmd = &cobra.Command{
		Use:   "check",
		Short: "Check things referenced in a config exist on GitHub. If something doesn't exist, will exit with rc 2.",
		Run: func(cmd *cobra.Command, args []string) {
			doCheck()
		},
	}
)

func init() {
	rootCmd.AddCommand(checkCmd)
}

func doCheck() {
	whoAmI()

	gh := pkg.NewGitHubinator(pkg.NewLogger()).WithToken(cfg.PAT)

	for _, w := range cfg.Watches {
		for _, r := range w.Repositories {
			if err := gh.CheckRepository(ctx, r); err != nil {
				fmt.Println(err)

				switch err.(type) {
				case pkg.GitHubNotFoundError:
					os.Exit(2)
				default:
					os.Exit(1)
				}
			}
		}
	}
}
