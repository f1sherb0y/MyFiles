// MyFiles is a fast, compact file manager.
package main

import (
	"fmt"
	"os"

	"myfiles/internal/app"
	"myfiles/internal/assochash"
	"myfiles/internal/cli"
	"myfiles/internal/content"
	"myfiles/internal/ghostty"
	"myfiles/internal/index"
	"myfiles/internal/search"
	"myfiles/internal/shell"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "--index-helper" {
		// Started as administrator by the program itself, to read the
		// drives' file tables: it writes their indexes and is done.
		os.Exit(index.Helper(os.Args[2], os.Args[3:]))
	}
	if len(os.Args) == 2 && os.Args[1] == shell.ProgramsFlag {
		// Started by the program itself to list what can be started: the
		// parts of the system that know stay out of the program that
		// stays open.
		if shell.ListPrograms(os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
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
		fmt.Print("\n\nLertaro (the grammar, matching and order of searches, and the table of pinyin)\n\n", search.License)
		fmt.Print("\n\ngithub.com/ledongthuc/pdf (reading the text of PDFs)\n\n", content.LicensePDF)
		fmt.Print("\n\nGhostty (the terminal's emulator, libghostty-vt)\n\n", ghostty.License)
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
