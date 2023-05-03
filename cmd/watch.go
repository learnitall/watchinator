package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/learnitall/watchinator/pkg"
	"github.com/spf13/cobra"
)

var (
	defaultPollInterval = time.Hour * time.Duration(1)
	watchCmd            = &cobra.Command{
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
	watchinator := pkg.NewWatchinator(logger, getGitHubinator, pollinator, configinator)

	if err := watchinator.Watch(ctx, configFilePath); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
