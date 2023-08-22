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
	configFilePath string

	gitHubRetries    int
	gitHubTimeoutSec int

	ctx = context.Background()
	cfg *pkg.Config

	rootCmd = &cobra.Command{
		Use:   "watchinator",
		Short: "Watch GitHub issues and prs based on labels",
	}
)

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().BoolVar(
		&pkg.DefaultLogOptions.LogShowTime, "log-time", pkg.DefaultLogOptions.LogShowTime,
		"Toggle timestamps to log messages",
	)
	rootCmd.PersistentFlags().BoolVar(
		&pkg.DefaultLogOptions.LogUseJSON, "log-json", pkg.DefaultLogOptions.LogUseJSON,
		"Toggle JSON-formatted log messages",
	)
	rootCmd.PersistentFlags().BoolVar(
		&pkg.DefaultLogOptions.LogVerbose, "verbose", pkg.DefaultLogOptions.LogVerbose,
		"Toggle extended debug log messages",
	)
	rootCmd.PersistentFlags().IntVar(
		&gitHubRetries, "gh-retries", 3, "Number of times requests to GitHub should be retried on failure",
	)
	rootCmd.PersistentFlags().IntVar(
		&gitHubTimeoutSec, "gh-timeout", 5*60, "Maximum number of seconds a request to GitHub can take",
	)
	rootCmd.PersistentFlags().StringVar(
		&configFilePath, "config", "/opt/watchinator/config.yaml", "Path to config file",
	)
}

func getGitHubinator() pkg.GitHubinator {
	return pkg.NewGitHubinator(pkg.NewLogger()).
		WithRetries(gitHubRetries).
		WithTimeout(time.Duration(gitHubTimeoutSec) * time.Second)
}

func getEmailinator() pkg.Emailinator {
	return pkg.NewEmailinator(pkg.NewLogger())
}

// initConfigOrDie reads the cmd's configFilePath variable and loads it into the cmd's cfg variable.
// If an error occurs, print it exit with rc 1.
func initConfigOrDie() {
	var err error

	cfg, err = pkg.NewConfigFromFile(configFilePath)
	if err != nil {
		fmt.Printf("unable to load config from %s: %s\n", configFilePath, err)
		os.Exit(1)
	}

	pkg.NewLogger().Debug("loaded config", "path", configFilePath)
}

// validateConfigOrDie calls cfg.Validate.
// If an error occurs, print it and exit with rc 1.
func validateConfigOrDie() {
	gh := getGitHubinator()
	e := getEmailinator()

	if err := cfg.Validate(context.Background(), gh, e); err != nil {
		fmt.Printf("unable to validate config: %s\n", err.Error())
		os.Exit(1)
	}
}
