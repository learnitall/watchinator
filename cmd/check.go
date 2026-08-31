package cmd

import (
	"errors"
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

// exitForCheckError reports rc 2 only when GitHub said the entity does not
// exist. Anything else (auth, rate limiting, network) is rc 1: the check could
// not be made, which is not the same answer as "missing".
func exitForCheckError(err error) {
	fmt.Println(err)

	var notFound *pkg.GitHubNotFoundError
	if errors.As(err, &notFound) {
		os.Exit(2)
	}

	os.Exit(1)
}

func doCheck() {
	whoAmI()

	gh := pkg.NewGitHubinator(pkg.NewLogger()).WithToken(cfg.PAT)

	for _, w := range cfg.Watches {
		for _, r := range w.Repositories {
			if err := gh.CheckRepository(ctx, r); err != nil {
				exitForCheckError(err)
			}
		}

		for _, o := range w.Organizations {
			if err := gh.CheckOrganization(ctx, o); err != nil {
				exitForCheckError(err)
			}
		}
	}
}
