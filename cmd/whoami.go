package cmd

import (
	"fmt"
	"os"

	"github.com/learnitall/watchinator/pkg"
	"github.com/spf13/cobra"
)

var (
	whoAmICmd = &cobra.Command{
		Use:   "whoami",
		Short: "Test authentication for user in config",
		Run: func(cmd *cobra.Command, args []string) {
			whoAmI()
		},
	}
)

func init() {
	rootCmd.AddCommand(whoAmICmd)
}

func whoAmI() {
	initConfigOrDie()

	if err := cfg.LoadPATFile(ctx); err != nil {
		fmt.Println(err)

		os.Exit(1)
	}

	logger := pkg.NewLogger()

	user, pat := cfg.User, cfg.PAT

	if len(user) == 0 || len(pat) == 0 {
		fmt.Println("need both user and pat")

		os.Exit(1)
	}

	logger.Debug("Checking PAT", "user", user)

	gh := getGitHubinator().WithToken(pat)

	_, err := gh.WhoAmI(ctx)
	if err != nil {
		fmt.Printf("Unable to authenticate with GitHub: %s\n", err)

		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Hello %s!", user))
}
