package tui_cmd

import (
	"context"
	"glesha/database"
	"glesha/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func Execute(ctx context.Context, args []string) error {
	dbPath, err := database.GetDBFilePath(ctx)
	if err != nil {
		return err
	}
	db, err := database.NewDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	app := tui.NewApp(ctx, db)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
