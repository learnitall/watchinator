package cmd

import (
	"bytes"
	"fmt"
	"os"

	"github.com/goccy/go-json"
	"github.com/spf13/cobra"
)

var (
	listCmd = &cobra.Command{
		Use:   "list watch_name",
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
	searchFilter := watch.GetSearchFilter()

	items, err := gh.ListIssues(ctx, searchFilter, matcher)
	if err != nil {
		fmt.Printf("unable to list items: %s\n", err)
		os.Exit(1)
	}

	buf := bytes.Buffer{}
	buf.WriteString("[")

	for i, item := range items {
		marshalled, err := json.Marshal(item)
		if err != nil {
			fmt.Printf("unable to marshal item to json '%+v': %s\n", item, err)

			return
		}

		buf.Write(marshalled)

		if i != len(items)-1 {
			buf.WriteString(",")
		}
	}

	buf.WriteString("]")
	fmt.Println(buf.String())
}
