package tui_cmd

import L "glesha/logger"

const usageStr string = `
USAGE
glesha tui

DESCRIPTION
Launches the interactive Terminal User Interface for browsing tasks.
`

func PrintUsage() {
	L.Print(usageStr)
}
