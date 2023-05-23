package main

import "github.com/learnitall/watchinator/cmd"

func main() {
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
