package main

import (
	"os/exec"
	"os"
	"log"
	"strings"
)

func clean() {
	cmd := "find /vol/win/batch/MedRxProvData " +
		   "-type d ! -newerct \"$OLDEST_DATE\" " +
		   "-name \"20[0-1][0-9][0-9][0-9][0-9][0-9]\" " +
		   "-exec ls -dv {} 2>&1 $LOG_DIR \\;"
	exec.Command(cmd).Output()
}

func dry_run() {
	cmd := "find /vol/win/batch/MedRxProvData " +
		   "-type d ! -newerct \"$OLDEST_DATE\" " +
		   "-name \"20[0-1][0-9][0-9][0-9][0-9][0-9]\" " +
		   "-exec ls -dv {} 2>&1 $LOG_DIR \\;"
	exec.Command(cmd).Output()
}

func help() {
	const HelpStr =
		`Usage: ./clean.sh: 
		  -d,  --dry-run	Returns matches only (No Delete)
		  -h,  --help		N/A
		  -j,  --job		Run as task 
		  -l,  --no-log     Default is to log a list of deleted matches.`
	println(HelpStr)
}

func main() {
	if len(os.Args) == 0 {
		// TODO: Display Y/N confirmation.
		clean()
	}

	switch strings.ToLower(os.Args[1]) { // Not case sensitive
		case "dry-run":
		case "d":
			dry_run()
			break
		case "help":
		case "h":
			help()
			break
		default:
			log.Fatal("Usage: Did not understand your request.")
			break
	}
	os.Exit(0)
}