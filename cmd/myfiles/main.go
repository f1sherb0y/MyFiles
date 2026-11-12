// MyFiles is a fast, compact file manager.
package main

import (
	"fmt"
	"os"

	"myfiles/internal/app"
	"myfiles/internal/assochash"
	"myfiles/internal/cli"
	"myfiles/internal/shell"
)

func main() {
	profile()
	cwd, _ := os.Getwd()
	cmd, err := cli.Parse(os.Args[1:], cwd)
	if err != nil {
		attachConsole()
		fmt.Fprintf(os.Stderr, "myfiles: %v\n\n%s", err, cli.Usage)
		os.Exit(2)
	}
	switch {
	case cmd.Help:
		attachConsole()
		fmt.Print(cli.Usage)
		return
	case cmd.Version:
		attachConsole()
		fmt.Println("myfiles", app.Version)
		return
	case cmd.Licenses:
		attachConsole()
		fmt.Print("UserChoiceLatestHash (cssxn)\n\n", assochash.License)
		return
	case cmd.SetDefault != "":
		attachConsole()
		exe, err := os.Executable()
		if err == nil {
			on := cmd.SetDefault == "on"
			err = shell.SetDefaultFileManager(exe, shell.Default{Folders: on, Shortcut: on})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "myfiles:", err)
			os.Exit(1)
		}
		if cmd.SetDefault == "on" {
			fmt.Println("MyFiles now opens folders, and Win+E.")
		} else {
			fmt.Println("Folders open in File Explorer again.")
		}
		return
	}
	if cmd.Bench != "" || cmd.Screenshot != "" {
		attachConsole()
	}
	if err := app.Run(cmd); err != nil {
		attachConsole()
		fmt.Fprintln(os.Stderr, "myfiles:", err)
		os.Exit(1)
	}
}
