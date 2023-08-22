package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/learnitall/watchinator/pkg"
)

var (
	watchCmd = &cobra.Command{
		Use:   "watch",
		Short: "Watch things on GitHub and subscribe to them",
		Run: func(cmd *cobra.Command, args []string) {
			doWatch()
		},
	}
)

func init() {
	rootCmd.AddCommand(watchCmd)
}

func doWatch() {
	ctx := context.Background()
	logger := pkg.NewLogger()
	configinator := pkg.NewConfiginator(logger)
	pollinator := pkg.NewPollinator(ctx, logger)
	emailinator := pkg.NewEmailinator(logger)
	gitHubinator := pkg.NewGitHubinator(logger)
	watchinator := pkg.NewWatchinator(logger, gitHubinator, pollinator, configinator, emailinator)

	go pkg.ServePromEndpoint(ctx)

	if err := watchinator.Watch(ctx, configFilePath); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
