package cmd

import (
	"bytes"
	"fmt"
	"os"

	"github.com/goccy/go-json"
	"github.com/spf13/cobra"
)

var (
	watchName string
	listCmd   = &cobra.Command{
		Use:   "list",
		Short: "List things on GitHub using the provided config.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cobra.MinimumNArgs(1)(cmd, args); err != nil {
				fmt.Println(err.Error())

				os.Exit(1)
			}

			doList(args[0])
		},
	}
)

func init() {
	rootCmd.AddCommand(listCmd)
}

func doList(watchName string) {
	initConfigOrDie()

	validateConfigOrDie()

	gh := getGitHubinator().WithToken(cfg.PAT)

	watch := cfg.GetWatch(watchName)
	if watch == nil {
		fmt.Printf("unknown watch with name '%s'\n", watchName)
		os.Exit(1)
	}

	matcher := watch.GetMatchinator()
	issueFilter := watch.GetIssueFilter()
	numRepos := len(watch.Repositories)

	buf := bytes.Buffer{}
	buf.WriteString("[")

	for repoIndex, r := range watch.Repositories {
		issues, err := gh.ListIssues(ctx, r, issueFilter, matcher)
		if err != nil {
			fmt.Printf("unable to list issues: %s\n", err)
			os.Exit(1)
		}

		numIssues := len(issues)

		for issueIndex, issue := range issues {
			marshalled, err := json.Marshal(issue)
			if err != nil {
				fmt.Printf("unable to marshal issue to json '%+v': %s\n", issue, err)

				return
			}

			buf.Write(marshalled)

			if repoIndex == numRepos-1 && issueIndex != numIssues-1 {
				buf.WriteString(",")
			}
		}
	}

	buf.WriteString("]")
	fmt.Println(buf.String())

	return
}
